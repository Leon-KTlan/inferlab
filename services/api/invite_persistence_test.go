package main

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInviteAuthLimitsAndSharesPersistAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.db")
	app, err := newApp(path, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	owner, run := seedQueuedLocalRun(t, app)
	run.Status = "succeeded"
	if err = app.store.saveRun(run, nil, ""); err != nil {
		t.Fatal(err)
	}
	token, err := app.store.share(owner, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if ok, err := app.store.authAttempt([]string{"persistent-bucket"}); err != nil || !ok {
			t.Fatal(err)
		}
	}
	app.Close()
	app, err = newApp(path, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if ok, err := app.store.authAttempt([]string{"persistent-bucket"}); err != nil || ok {
		t.Fatal("restart erased auth rate limit", err)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/shares/"+token, nil))
	if w.Code != 200 {
		t.Fatal("restart erased share")
	}
	if err = app.store.revokeShare(owner, run.RunID); err != nil {
		t.Fatal(err)
	}
	if err = app.store.migrate(); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/shares/"+token, nil))
	if w.Code != 404 {
		t.Fatal("migration resurrected revoked share")
	}
	if _, err = app.store.db.Exec("UPDATE login_attempts SET window_start=?", time.Now().Add(-11*time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if ok, err := app.store.authAttempt([]string{"persistent-bucket"}); err != nil || !ok {
		t.Fatal("expired limit not cleaned", err)
	}
}

func TestInviteClaimSerializesConcurrentRunCreation(t *testing.T) {
	app, _ := inviteApp(t)
	owner, run := seedQueuedLocalRun(t, app)
	run.Status = "succeeded"
	if err := app.store.saveRun(run, nil, ""); err != nil {
		t.Fatal(err)
	}
	before, err := app.store.quota(owner)
	if err != nil {
		t.Fatal(err)
	}
	user := newID("user")
	if err = app.store.registerAccount(identity{Owner: owner}, authInput{Username: "claim_quota", ClaimAnonymous: true}, user, []byte("unused-fixture-hash"), hashToken("unused-fixture-session")); err != nil {
		t.Fatal(err)
	}
	after, err := app.store.quota(user)
	if err != nil || before.Reserved != after.Reserved {
		t.Fatal("claim lost reservation", before, after, err)
	}
	// A request that was authorized before claim cannot insert a Run afterwards.
	run.RunID = newID("run")
	run.Status = "queued"
	if err = app.store.createRun(owner, run); err == nil {
		t.Fatal("stale creation survived claim")
	}
	after, err = app.store.quota(user)
	if err != nil || after.Reserved != before.Reserved {
		t.Fatal("failed stale request charged new owner", err)
	}
	// No password, invitation or runner key is added to serialized user records.
	var value []byte
	if err = app.store.db.QueryRow("SELECT data FROM runs WHERE experiment_id=?", run.ExperimentID).Scan(&value); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	for key := range decoded {
		if strings.Contains(key, "password") || strings.Contains(key, "invite") || strings.Contains(key, "api_key") {
			t.Fatal("credential field persisted", key)
		}
	}
}
