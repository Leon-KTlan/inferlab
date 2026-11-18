package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInviteRejectedAuthDoesNotCreateSessions(t *testing.T) {
	app, _ := inviteApp(t)
	for _, path := range []string{"login", "register"} {
		for i := 0; i < 12; i++ {
			body := `{"username":"missing_user","password":"test-password-fixture"}`
			want := 401
			if path == "register" {
				body = `{"username":"missing_user","password":"test-password-fixture","invite":"wrong-fixture"}`
				want = 403
			}
			if i >= 5 {
				want = 429
			}
			req := httptest.NewRequest("POST", "/api/v1/auth/"+path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			app.ServeHTTP(w, req)
			if w.Code != want {
				t.Fatalf("%s attempt %d got %d want %d", path, i, w.Code, want)
			}
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("rejected credentials issued a persistent session")
			}
		}
	}
	var sessions, buckets int
	if err := app.store.db.QueryRow("SELECT count(*) FROM sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := app.store.db.QueryRow("SELECT count(*) FROM login_attempts").Scan(&buckets); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || buckets > 3 {
		t.Fatalf("rejected no-cookie auth grew storage: sessions=%d buckets=%d", sessions, buckets)
	}
}

func TestInviteSessionCapacityAndCleanup(t *testing.T) {
	app, _ := inviteApp(t)
	_, err := app.store.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<4096)
INSERT INTO sessions(token_hash,owner,expires_at) SELECT printf('fixture-%d',x),printf('owner-%d',x),? FROM n`, time.Now().Add(time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/auth/me", nil))
		if w.Code != 503 {
			t.Fatalf("anonymous session cap not enforced: %d", w.Code)
		}
	}
	var count int
	if err = app.store.db.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 4096 {
		t.Fatal("rejected anonymous request grew table", count, err)
	}
	// Anonymous saturation leaves room for an authenticated session.
	req := httptest.NewRequest("POST", "/api/v1/auth/register", strings.NewReader(`{"username":"capacity_user","password":"test-password-fixture","invite":"test-invite-fixture"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatal("anonymous saturation blocked authenticated capacity", w.Code)
	}
	if _, err = app.store.db.Exec("UPDATE sessions SET expires_at=0 WHERE rowid<=64"); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/auth/me", nil))
	if w.Code != 200 {
		t.Fatal("expired sessions did not release capacity", w.Code)
	}
	if err = app.store.db.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 4034 {
		t.Fatal("bounded cleanup failed", count, err)
	}
}
