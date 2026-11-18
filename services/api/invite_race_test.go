package main

import (
	"database/sql"
	"errors"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

func TestInviteRevokeClaimTransactionRace(t *testing.T) {
	app, _ := inviteApp(t)
	owner, run := seedQueuedLocalRun(t, app)
	run.Status = "succeeded"
	if err := app.store.saveRun(run, nil, ""); err != nil {
		t.Fatal(err)
	}
	token, err := app.store.share(owner, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	// Hold the production transaction across the same read/claim/write ordering
	// that previously allowed a false successful revocation.
	tx, err := app.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var authorized string
	if err = tx.QueryRow("SELECT owner FROM runs WHERE id=? AND owner=?", run.RunID, owner).Scan(&authorized); err != nil {
		t.Fatal(err)
	}
	waitCount := app.store.db.Stats().WaitCount
	claimed := make(chan error, 1)
	userID := newID("user")
	go func() {
		claimed <- app.store.registerAccount(identity{Owner: owner}, authInput{Username: "race_claim", ClaimAnonymous: true}, userID, []byte("fixture-unused-hash"), hashToken("fixture-session"))
	}()
	deadline := time.Now().Add(2 * time.Second)
	for app.store.db.Stats().WaitCount == waitCount && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if app.store.db.Stats().WaitCount == waitCount {
		t.Fatal("claim did not attempt to interleave")
	}
	if err = revokeReportShare(tx, owner, run.RunID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-claimed; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/shares/"+token, nil)
	app.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatal("successful revoke left claimed share public")
	}
	// Reverse ordering: a claim that wins must make stale-owner revoke fail.
	if _, err = app.store.share(userID, run.RunID); err != nil {
		t.Fatal(err)
	}
	if err = app.store.revokeShare(owner, run.RunID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("stale owner got false success", err)
	}
	if err = app.store.revokeShare(userID, run.RunID); err != nil {
		t.Fatal(err)
	}
}
