package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Quota struct {
	Limit     int       `json:"limit"`
	Reserved  int       `json:"reserved"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"reset_at"`
}
type quotaError struct{ Quota Quota }

func (e *quotaError) Error() string { return "daily request reservation quota exceeded" }

func quotaValue(limit, reserved int, now time.Time) Quota {
	now = now.UTC()
	remaining := limit - reserved
	if remaining < 0 {
		remaining = 0
	}
	return Quota{Limit: limit, Reserved: reserved, Remaining: remaining, ResetAt: time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)}
}

func (s *Store) quota(owner string) (Quota, error) {
	var reserved int
	now := s.now().UTC()
	err := s.db.QueryRow("SELECT reserved FROM daily_quotas WHERE owner=? AND day=?", owner, now.Format("2006-01-02")).Scan(&reserved)
	if isNotFound(err) {
		err = nil
	}
	return quotaValue(s.dailyLimit, reserved, now), err
}

func (s *Store) reserveRun(owner string, run Run) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = unclaimedOwner(tx, owner); err != nil {
		return err
	}
	var experimentOwner string
	if err = tx.QueryRow("SELECT owner FROM experiments WHERE id=?", run.ExperimentID).Scan(&experimentOwner); err != nil {
		return err
	}
	if owner != experimentOwner {
		return sql.ErrNoRows
	}
	if run.Execution != nil {
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM runs WHERE experiment_id=?", run.ExperimentID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return errExecutionSubmitted
		}
	}
	var active, global int
	if err = tx.QueryRow("SELECT count(*),COALESCE(sum(CASE WHEN owner=? THEN 1 ELSE 0 END),0) FROM runs WHERE status IN ('queued','running')", owner).Scan(&global, &active); err != nil {
		return err
	}
	if active > 0 || global >= 4 {
		return errBusy
	}

	if run.SourceType == "openai_compatible" {
		rows, err := tx.Query("SELECT data FROM runs WHERE status IN ('queued','running')")
		if err != nil {
			return err
		}
		busy := false
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				rows.Close()
				return err
			}
			var other Run
			if err := json.Unmarshal(data, &other); err != nil {
				rows.Close()
				return err
			}
			if other.SourceType == "openai_compatible" && (run.Execution != nil || other.Execution != nil) && normalizedTarget(other.Config.Target.Endpoint) == normalizedTarget(run.Config.Target.Endpoint) || run.Execution != nil && run.Execution.Mode == "platform" && other.Execution != nil && other.Execution.Mode == "platform" {
				busy = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if busy {
			return errBusy
		}
	}
	now := s.now().UTC()
	day := now.Format("2006-01-02")
	var reserved int
	err = tx.QueryRow("SELECT reserved FROM daily_quotas WHERE owner=? AND day=?", owner, day).Scan(&reserved)
	if err != nil && !isNotFound(err) {
		return err
	}
	if run.Config.Workload.MaxRequests > s.dailyLimit-reserved {
		return &quotaError{Quota: quotaValue(s.dailyLimit, reserved, now)}
	}
	if _, err = tx.Exec("INSERT INTO daily_quotas(owner,day,reserved) VALUES(?,?,?) ON CONFLICT(owner,day) DO UPDATE SET reserved=reserved+excluded.reserved", owner, day, run.Config.Workload.MaxRequests); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO runs(id,experiment_id,owner,status,data) VALUES(?,?,?,?,?)", run.RunID, run.ExperimentID, owner, run.Status, data); err != nil {
		return err
	}
	return tx.Commit()
}

var errActiveDelete = errors.New("active runs cannot be deleted")
var errReportNotFinished = errors.New("only terminal reports can be shared")

func (s *Store) share(owner, runID string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRow("SELECT status FROM runs WHERE id=? AND owner=?", runID, owner).Scan(&status); err != nil {
		return "", err
	}
	if status == "queued" || status == "running" {
		return "", errReportNotFinished
	}
	token := newID("share")
	if _, err = tx.Exec("INSERT OR IGNORE INTO report_shares(token,run_id,owner,created_at) VALUES(?,?,?,?)", token, runID, owner, s.now().Unix()); err != nil {
		return "", err
	}
	if err = tx.QueryRow("SELECT token FROM report_shares WHERE run_id=? AND owner=?", runID, owner).Scan(&token); err != nil {
		return "", err
	}
	return token, tx.Commit()
}

func (s *Store) revokeShare(owner, runID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = revokeReportShare(tx, owner, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func revokeReportShare(tx *sql.Tx, owner, runID string) error {
	var id string
	if err := tx.QueryRow("SELECT id FROM runs WHERE id=? AND owner=?", runID, owner).Scan(&id); err != nil {
		return err
	}
	_, err := tx.Exec("DELETE FROM report_shares WHERE run_id=? AND owner=?", runID, owner)
	return err
}

func (s *Store) deleteRecords(owner, id string, experiment bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	table, column := "runs", "id"
	if experiment {
		table, column = "experiments", "experiment_id"
	}
	var found string
	if err = tx.QueryRow("SELECT id FROM "+table+" WHERE id=? AND owner=?", id, owner).Scan(&found); err != nil {
		return err
	}
	var active int
	if err = tx.QueryRow("SELECT count(*) FROM runs WHERE "+column+"=? AND owner=? AND status IN ('queued','running')", id, owner).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return errActiveDelete
	}
	for _, child := range []string{"report_shares", "samples", "events"} {
		if _, err = tx.Exec("DELETE FROM "+child+" WHERE run_id IN (SELECT id FROM runs WHERE "+column+"=? AND owner=?)", id, owner); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("DELETE FROM runs WHERE "+column+"=? AND owner=?", id, owner); err != nil {
		return err
	}
	if experiment {
		if _, err = tx.Exec("DELETE FROM experiments WHERE id=? AND owner=?", id, owner); err != nil {
			return err
		}
	}
	return tx.Commit()
}
