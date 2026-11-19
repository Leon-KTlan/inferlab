package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db           *sql.DB
	dailyLimit   int
	now          func() time.Time
	taskFailures atomic.Uint64
}

var errRunAlreadyFinished = errors.New("run is already terminal")

const currentSchemaVersion = 3

func openStore(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, dailyLimit: 10000, now: time.Now}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if path != ":memory:" {
		if err := os.Chmod(path, 0600); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec("PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;"); err != nil {
		return err
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("unsupported database schema version %d", version)
	}
	for version < currentSchemaVersion {
		next := version + 1
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if err := applySchemaMigration(tx, next); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", next)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = next
	}
	return nil
}

func applySchemaMigration(tx *sql.Tx, version int) error {
	switch version {
	case 3:
		_, err := tx.Exec(`ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'user' CHECK(role IN ('user','admin'));
CREATE UNIQUE INDEX users_single_admin ON users(role) WHERE role='admin';
CREATE TABLE registration_settings (id INTEGER PRIMARY KEY CHECK(id=1), enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), invite_hash TEXT NOT NULL);
CREATE TABLE admin_audit (id INTEGER PRIMARY KEY, operation TEXT NOT NULL, actor_id TEXT NOT NULL REFERENCES users(id), at INTEGER NOT NULL, enabled INTEGER NOT NULL CHECK(enabled IN (0,1)));`)
		return err
	case 2:
		_, err := tx.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash BLOB NOT NULL);
CREATE TABLE sessions (token_hash TEXT PRIMARY KEY, owner TEXT NOT NULL, user_id TEXT REFERENCES users(id), expires_at INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0);
CREATE INDEX sessions_owner ON sessions(owner);
CREATE TABLE anonymous_claims (owner TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id));
CREATE TABLE login_attempts (bucket TEXT PRIMARY KEY, window_start INTEGER NOT NULL, failures INTEGER NOT NULL);
CREATE TABLE daily_quotas (owner TEXT NOT NULL, day TEXT NOT NULL, reserved INTEGER NOT NULL CHECK(reserved>=0), PRIMARY KEY(owner,day));
CREATE TABLE report_shares (token TEXT PRIMARY KEY, run_id TEXT NOT NULL UNIQUE REFERENCES runs(id), owner TEXT NOT NULL, created_at INTEGER NOT NULL);`)
		return err
	case 1:
		_, err := tx.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value BLOB NOT NULL);
CREATE TABLE experiments (id TEXT PRIMARY KEY, owner TEXT NOT NULL, data BLOB NOT NULL);
CREATE INDEX experiments_owner ON experiments(owner);
CREATE TABLE runs (id TEXT PRIMARY KEY, experiment_id TEXT NOT NULL REFERENCES experiments(id), owner TEXT NOT NULL, status TEXT NOT NULL, data BLOB NOT NULL);
CREATE INDEX runs_owner ON runs(owner);
CREATE TABLE samples (id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL REFERENCES runs(id), stage INTEGER NOT NULL, data BLOB NOT NULL);
CREATE INDEX samples_run ON samples(run_id);
CREATE TABLE events (id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL REFERENCES runs(id), data BLOB NOT NULL);
	CREATE INDEX events_run ON events(run_id);`)
		return err
	default:
		return fmt.Errorf("no migration registered for schema version %d", version)
	}
}

func (s *Store) localSecret(newSecret []byte) ([]byte, error) {
	if _, err := s.db.Exec("INSERT OR IGNORE INTO settings(key,value) VALUES('session_secret',?)", newSecret); err != nil {
		return nil, err
	}
	var value []byte
	err := s.db.QueryRow("SELECT value FROM settings WHERE key='session_secret'").Scan(&value)
	return value, err
}

func (s *Store) createExperiment(owner string, experiment Experiment) error {
	data, err := json.Marshal(experiment)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := unclaimedOwner(tx, owner); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO experiments(id,owner,data) VALUES(?,?,?)", experiment.ExperimentID, owner, data); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) experiment(owner, id string) (Experiment, error) {
	var e Experiment
	var data []byte
	err := s.db.QueryRow("SELECT data FROM experiments WHERE owner=? AND id=?", owner, id).Scan(&data)
	if err != nil {
		return e, err
	}
	err = json.Unmarshal(data, &e)
	return e, err
}

func (s *Store) experiments(owner string) ([]Experiment, error) {
	rows, err := s.db.Query("SELECT data FROM experiments WHERE owner=? ORDER BY rowid DESC LIMIT 100", owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Experiment{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var e Experiment
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

func (s *Store) createRun(owner string, r Run) error {
	return s.reserveRun(owner, r)
}

func (s *Store) run(owner, id string) (Run, error) {
	var r Run
	var data []byte
	err := s.db.QueryRow("SELECT data FROM runs WHERE owner=? AND id=?", owner, id).Scan(&data)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(data, &r)
	return r, err
}

func (s *Store) runOwner(id string) (string, error) {
	var owner string
	err := s.db.QueryRow("SELECT owner FROM runs WHERE id=?", id).Scan(&owner)
	return owner, err
}

func (s *Store) runs(owner string) ([]Run, error) {
	rows, err := s.db.Query("SELECT data FROM runs WHERE owner=? ORDER BY rowid DESC LIMIT 100", owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := []Run{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var run Run
		if err := json.Unmarshal(data, &run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) saveRun(r Run, sample *Sample, eventType string) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE runs SET status=?,data=? WHERE id=?", r.Status, data, r.RunID); err != nil {
		return err
	}
	if sample != nil {
		data, err = json.Marshal(sample)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO samples(run_id,stage,data) VALUES(?,?,?)", r.RunID, sample.Stage, data); err != nil {
			return err
		}
	}
	if eventType != "" {
		data, err = json.Marshal(Event{RunID: r.RunID, Type: eventType, Stage: r.CurrentStage, At: time.Now().UTC()})
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO events(run_id,data) VALUES(?,?)", r.RunID, data); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if r.Status == "failed" && eventType != "" {
		s.taskFailures.Add(1)
	}
	return nil
}

func (s *Store) finishLocalRun(owner string, run Run, eventType string) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Registration can transfer an in-flight task after its request was authorized.
	owner, err = currentOwner(tx, owner)
	if err != nil {
		return err
	}
	result, err := tx.Exec("UPDATE runs SET status=?,data=? WHERE id=? AND owner=? AND status IN ('queued','running')", run.Status, data, run.RunID, owner)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return errRunAlreadyFinished
	}
	event, err := json.Marshal(Event{RunID: run.RunID, Type: eventType, Stage: run.CurrentStage, At: time.Now().UTC()})
	if err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO events(run_id,data) VALUES(?,?)", run.RunID, event); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if run.Status == "failed" {
		s.taskFailures.Add(1)
	}
	return nil
}

func (s *Store) events(runID string, after int64) ([]Event, error) {
	rows, err := s.db.Query("SELECT id,data FROM events WHERE run_id=? AND id>? ORDER BY id LIMIT 200", runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var e Event
		var data []byte
		var id int64
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		e.ID = id
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) recoverRuns() error {
	rows, err := s.db.Query("SELECT data FROM runs WHERE status IN ('queued','running')")
	if err != nil {
		return err
	}
	var runs []Run
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			rows.Close()
			return err
		}
		var r Run
		if err := json.Unmarshal(data, &r); err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range runs {
		now := time.Now().UTC()
		r.Status = "failed"
		r.ErrorCode = "RUN_INTERRUPTED"
		r.FinishedAt = &now
		if err := s.saveRun(r, nil, "run_interrupted"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) healthy(ctx context.Context) error { return s.db.PingContext(ctx) }
func isNotFound(err error) bool                    { return errors.Is(err, sql.ErrNoRows) }
