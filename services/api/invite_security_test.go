package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func inviteApp(t *testing.T) (*App, *httptest.Server) {
	t.Helper()
	t.Setenv("INVITE_CODE", "test-invite-fixture")
	app, err := newApp(":memory:", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return app, srv
}

func registerFixture(t *testing.T, c *http.Client, base, name string, claim bool) {
	t.Helper()
	request(t, c, "POST", base+"/api/v1/auth/register", map[string]any{"username": name, "password": "test-password-fixture", "invite": "test-invite-fixture", "claim_anonymous": claim}, 201, nil)
}

func sessionValue(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range c.Jar.Cookies(u) {
		if cookie.Name == "inferlab_session" {
			return cookie.Value
		}
	}
	t.Fatal("missing session cookie")
	return ""
}

func TestInviteJSONOriginAndPasswordBounds(t *testing.T) {
	_, srv := inviteApp(t)
	for _, path := range []string{"register", "login", "logout"} {
		for _, tc := range []struct {
			origin, content string
			status          int
		}{{"https://evil.example", "application/json", 403}, {"null", "application/json", 403}, {"", "text/plain", 415}} {
			req := httptest.NewRequest("POST", "http://example.com/api/v1/auth/"+path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", tc.content)
			req.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			srv.Config.Handler.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("%s got %d want %d", path, w.Code, tc.status)
			}
		}
	}
	c := clientWithCookies()
	for _, password := range []string{strings.Repeat("x", 73), strings.Repeat("界", 25)} {
		request(t, c, "POST", srv.URL+"/api/v1/auth/register", map[string]any{"username": "bound", "password": password, "invite": "test-invite-fixture"}, 400, nil)
	}
	request(t, c, "POST", srv.URL+"/api/v1/auth/register", map[string]any{"username": "bound", "password": strings.Repeat("x", 72), "invite": "test-invite-fixture"}, 201, nil)
}

func TestInviteRegistrationClosedAndKDFRateBounded(t *testing.T) {
	app, srv := inviteApp(t)
	t.Setenv("INVITE_CODE", "")
	c := clientWithCookies()
	request(t, c, "POST", srv.URL+"/api/v1/auth/register", map[string]any{"username": "closed", "password": "test-password-fixture", "invite": "test-invite-fixture"}, 403, nil)
	// Random usernames are rejected by the IP bucket before a sixth KDF.
	for i := 0; i < 5; i++ {
		request(t, c, "POST", srv.URL+"/api/v1/auth/login", map[string]string{"username": fmt.Sprintf("unknown%d", i), "password": "test-password-fixture"}, 401, nil)
	}
	request(t, c, "POST", srv.URL+"/api/v1/auth/login", map[string]string{"username": "random-new", "password": "test-password-fixture"}, 429, nil)
	var n int
	if err := app.store.db.QueryRow("SELECT count(*) FROM login_attempts").Scan(&n); err != nil || n > 7 {
		t.Fatalf("unbounded buckets: %d %v", n, err)
	}
	for i := 0; i < 5; i++ {
		request(t, c, "POST", srv.URL+"/api/v1/auth/register", map[string]any{"username": fmt.Sprintf("closed%d", i), "password": "test-password-fixture", "invite": "test-invite-fixture"}, map[bool]int{true: 429, false: 403}[i == 4], nil)
	}
	// Username limit spans IPs, and the total bucket cap fails closed.
	if _, err := app.store.db.Exec("INSERT INTO login_attempts(bucket,window_start,failures) VALUES(?,?,5)", hashToken("login:user:shared"), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"shared","password":"test-password-fixture"}`))
	req.RemoteAddr = "192.0.2.2:1234"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != 429 {
		t.Fatal("username rate limit bypassed")
	}
	tx, err := app.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4096; i++ {
		if _, err = tx.Exec("INSERT OR IGNORE INTO login_attempts VALUES(?,?,1)", fmt.Sprintf("fixture-%d", i), time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ok, err := app.store.authAttempt([]string{"new-bucket"})
	if err != nil || ok {
		t.Fatal("bucket cap did not fail closed", err)
	}
}

func TestInviteSessionExpiryRevocationAndNoLoginMerge(t *testing.T) {
	app, srv := inviteApp(t)
	a, b := clientWithCookies(), clientWithCookies()
	registerFixture(t, a, srv.URL, "alice", false)
	old := sessionValue(t, a, srv.URL)
	var e Experiment
	request(t, a, "POST", srv.URL+"/api/v1/experiments", testConfig(), 201, &e)
	var anonymous Experiment
	request(t, b, "POST", srv.URL+"/api/v1/experiments", testConfig(), 201, &anonymous)
	request(t, b, "POST", srv.URL+"/api/v1/auth/login", map[string]string{"username": "alice", "password": "test-password-fixture"}, 200, nil)
	if sessionValue(t, b, srv.URL) == old {
		t.Fatal("login did not rotate session")
	}
	request(t, b, "GET", srv.URL+"/api/v1/experiments/"+e.ExperimentID, nil, 200, nil)
	request(t, b, "GET", srv.URL+"/api/v1/experiments/"+anonymous.ExperimentID, nil, 404, nil)
	loginToken := sessionValue(t, b, srv.URL)
	request(t, b, "POST", srv.URL+"/api/v1/auth/logout", map[string]any{}, 200, nil)
	replay := httptest.NewRequest("GET", "/api/v1/experiments/"+e.ExperimentID, nil)
	replay.AddCookie(&http.Cookie{Name: "inferlab_session", Value: loginToken})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, replay)
	if w.Code != 404 {
		t.Fatal("logout token replay succeeded")
	}
	if _, err := app.store.db.Exec("UPDATE sessions SET expires_at=0 WHERE token_hash=?", hashToken(old)); err != nil {
		t.Fatal(err)
	}
	request(t, a, "GET", srv.URL+"/api/v1/experiments/"+e.ExperimentID, nil, 404, nil)
	registerFixture(t, b, srv.URL, "bob", false)
	request(t, b, "GET", srv.URL+"/api/v1/experiments/"+e.ExperimentID, nil, 404, nil)
}

func TestInviteClaimActiveMockAndQuotaOnlyOnce(t *testing.T) {
	app, srv := inviteApp(t)
	c := clientWithCookies()
	var e Experiment
	request(t, c, "POST", srv.URL+"/api/v1/experiments", testConfig(), 201, &e)
	oldToken := sessionValue(t, c, srv.URL)
	var anon string
	if err := app.store.db.QueryRow("SELECT owner FROM sessions WHERE token_hash=?", hashToken(oldToken)).Scan(&anon); err != nil {
		t.Fatal(err)
	}
	var run Run
	request(t, c, "POST", srv.URL+"/api/v1/experiments/"+e.ExperimentID+"/runs", nil, 202, &run)
	registerFixture(t, c, srv.URL, "claimant", true)
	var me struct{ Quota Quota }
	request(t, c, "GET", srv.URL+"/api/v1/auth/me", nil, 200, &me)
	if me.Quota.Reserved != 100 {
		t.Fatal("claim reset quota", me.Quota)
	}
	request(t, c, "POST", srv.URL+"/api/v1/runs/"+run.RunID+"/cancel", nil, 202, nil)
	if got := waitRun(t, c, srv.URL, run.RunID); got.Status != "canceled" {
		t.Fatal("claim broke active cancellation", got.Status)
	}
	if err := app.store.createRun(anon, Run{RunID: newID("run"), ExperimentID: e.ExperimentID, Config: e.Config, Status: "queued"}); !errors.Is(err, errSessionChanged) {
		t.Fatal("stale anonymous write allowed", err)
	}
	err := app.store.registerAccount(identity{Owner: anon}, authInput{Username: "second", ClaimAnonymous: true}, newID("user"), []byte("unused-test-hash"), hashToken("fixture-second"))
	if !errors.Is(err, errSessionChanged) {
		t.Fatal("second claim succeeded", err)
	}
	req := httptest.NewRequest("GET", "/api/v1/runs/"+run.RunID, nil)
	req.AddCookie(&http.Cookie{Name: "inferlab_session", Value: oldToken})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatal("old anonymous cookie accessed claimed data")
	}
	request(t, c, "GET", srv.URL+"/api/v1/auth/me", nil, 200, &me)
	if me.Quota.Reserved != 100 {
		t.Fatal("cancellation refunded quota")
	}
}

func TestInviteClaimRunnerTokenStillWorksInSecureEnvironment(t *testing.T) {
	app, srv := inviteApp(t)
	owner, run := seedQueuedLocalRun(t, app)
	legacy := owner + "." + app.sign(owner)
	c := clientWithCookies()
	u, _ := url.Parse(srv.URL)
	c.Jar.SetCookies(u, []*http.Cookie{{Name: "inferlab_session", Value: legacy}})
	registerFixture(t, c, srv.URL, "runner_claim", true)
	if !app.credentials.put(run.RunID, "fixture-key-in-memory") {
		t.Fatal("credential unavailable")
	}
	app.secure = true
	for _, kind := range []string{"credential", "result"} {
		var body any = map[string]any{"target": run.Config.Target}
		if kind == "result" {
			body = validLocalResult(run.RunID, run.Config)
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/runner-tasks/"+run.RunID+"/"+kind, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-InferLab-Runner-Token", app.runnerToken(run.RunID))
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		want := 200
		if kind == "result" {
			want = 202
		}
		if w.Code != want {
			t.Fatalf("token %s got %d: %s", kind, w.Code, w.Body.String())
		}
	}
	current, err := app.store.runOwner(run.RunID)
	if err != nil || current == owner {
		t.Fatal("claim did not transfer runner")
	}
	got, err := app.store.run(current, run.RunID)
	if err != nil || got.Status != "succeeded" {
		t.Fatal("claimed task lost result", err)
	}
}

func TestInviteV1MigrationAndLegacyExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = applySchemaMigration(tx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	owner := newID("visitor")
	e := Experiment{ExperimentID: "legacy-exp", Config: testConfig()}
	data, _ := json.Marshal(e)
	if _, err = tx.Exec("INSERT INTO experiments VALUES(?,?,?)", e.ExperimentID, owner, data); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()
	app, err := newApp(path, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	legacy := owner + "." + app.sign(owner)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/v1/experiments/legacy-exp", nil)
		req.AddCookie(&http.Cookie{Name: "inferlab_session", Value: legacy})
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal("legacy data lost", w.Code)
		}
		if err = app.store.migrate(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = app.store.db.Exec("UPDATE sessions SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	if _, err = app.store.authAttempt([]string{"cleanup"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/experiments/legacy-exp", nil)
	req.AddCookie(&http.Cookie{Name: "inferlab_session", Value: legacy})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatal("expired legacy session resurrected")
	}
	app.Close()
	store, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	if _, err = store.experiment(owner, e.ExperimentID); err != nil {
		t.Fatal("restart lost v1 data", err)
	}
}

func TestInviteQuotaAtomicRollbackUTCAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota.db")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.dailyLimit = 100
	now := time.Date(2026, 11, 18, 23, 59, 59, 0, time.UTC)
	s.now = func() time.Time { return now }
	e := Experiment{ExperimentID: "quota-exp", Config: testConfig()}
	if err = s.createExperiment("quota-owner", e); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.createRun("quota-owner", Run{RunID: newID("run"), ExperimentID: e.ExperimentID, Status: "succeeded", Config: e.Config})
		}()
	}
	wg.Wait()
	close(results)
	success, exhausted := 0, 0
	for err := range results {
		var q *quotaError
		if err == nil {
			success++
		} else if errors.As(err, &q) {
			exhausted++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || exhausted != 1 {
		t.Fatalf("last quota race: success=%d exhausted=%d", success, exhausted)
	}
	q, err := s.quota("quota-owner")
	if err != nil || q.Reserved != 100 || !q.ResetAt.Equal(now.Add(time.Second)) {
		t.Fatal("invalid quota", q, err)
	}
	s.db.Close()
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	s.dailyLimit = 100
	s.now = func() time.Time { return now }
	q, err = s.quota("quota-owner")
	if err != nil || q.Reserved != 100 {
		t.Fatal("restart reset quota", err)
	}
	now = now.Add(time.Second)
	q, err = s.quota("quota-owner")
	if err != nil || q.Reserved != 0 {
		t.Fatal("UTC reset failed", err)
	}
	// An insertion failure after reservation must roll the quota back.
	if _, err = s.db.Exec("CREATE TRIGGER fail_run_insert BEFORE INSERT ON runs BEGIN SELECT RAISE(ABORT,'fixture insert failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err = s.createRun("quota-owner", Run{RunID: newID("run"), ExperimentID: e.ExperimentID, Status: "queued", Config: e.Config}); err == nil {
		t.Fatal("fixture must fail")
	}
	q, err = s.quota("quota-owner")
	if err != nil || q.Reserved != 0 {
		t.Fatal("failed creation charged quota", q, err)
	}
}

func TestInviteQuotaMockRunnerAndStructuredError(t *testing.T) {
	app, srv := inviteApp(t)
	app.store.dailyLimit = 150
	c := clientWithCookies()
	var e Experiment
	request(t, c, "POST", srv.URL+"/api/v1/experiments", testConfig(), 201, &e)
	var run Run
	request(t, c, "POST", srv.URL+"/api/v1/experiments/"+e.ExperimentID+"/runs", nil, 202, &run)
	waitRun(t, c, srv.URL, run.RunID)
	owner, err := app.store.runOwner(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig()
	config.Target = Target{Type: "openai_compatible", Endpoint: "https://203.0.113.1/v1", Model: "fixture-model"}
	local := Experiment{ExperimentID: newID("exp"), Config: config}
	if err = app.store.createExperiment(owner, local); err != nil {
		t.Fatal(err)
	}
	var failure struct {
		Error struct {
			Code  string
			Quota Quota
		}
	}
	request(t, c, "POST", srv.URL+"/api/v1/experiments/"+local.ExperimentID+"/runner-tasks", nil, 429, &failure)
	if failure.Error.Code != "DAILY_REQUEST_QUOTA_EXCEEDED" || failure.Error.Quota.Remaining != 50 || failure.Error.Quota.ResetAt.IsZero() {
		t.Fatal("missing structured quota error", failure)
	}
	config.Workload.MaxRequests = 50
	local = Experiment{ExperimentID: newID("exp"), Config: config}
	if err = app.store.createExperiment(owner, local); err != nil {
		t.Fatal(err)
	}
	var task RunnerTask
	request(t, c, "POST", srv.URL+"/api/v1/experiments/"+local.ExperimentID+"/runner-tasks", nil, 202, &task)
	request(t, c, "POST", srv.URL+"/api/v1/runs/"+task.RunID+"/cancel", nil, 202, nil)
	q, err := app.store.quota(owner)
	if err != nil || q.Reserved != 150 {
		t.Fatal("mock/runner quota diverged", q, err)
	}
}

func TestInviteSharesWhitelistRevocationIsolationAndDelete(t *testing.T) {
	app, srv := inviteApp(t)
	a, b := clientWithCookies(), clientWithCookies()
	var e Experiment
	config := testConfig()
	config.Name = "SECRET_NAME_SENTINEL"
	config.Target.Model = "SECRET_MODEL_SENTINEL"
	request(t, a, "POST", srv.URL+"/api/v1/experiments", config, 201, &e)
	var run Run
	request(t, a, "POST", srv.URL+"/api/v1/experiments/"+e.ExperimentID+"/runs", nil, 202, &run)
	for _, path := range []string{"/runs/" + run.RunID, "/experiments/" + e.ExperimentID} {
		request(t, a, "DELETE", srv.URL+"/api/v1"+path, nil, 409, nil)
	}
	request(t, a, "POST", srv.URL+"/api/v1/runs/"+run.RunID+"/share", nil, 409, nil)
	waitRun(t, a, srv.URL, run.RunID)
	owner, err := app.store.runOwner(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := app.store.run(owner, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Config.Target.Endpoint = "https://SECRET_ENDPOINT_SENTINEL.example/v1"
	stored.ErrorCode = "SECRET_ERROR_SENTINEL"
	stored.Diagnostics = []RequestDiagnostic{{ProviderCode: "SECRET_DIAGNOSTIC_SENTINEL"}}
	if err = app.store.saveRun(stored, nil, "SECRET_EVENT_SENTINEL"); err != nil {
		t.Fatal(err)
	}
	var share struct{ Token, Path string }
	request(t, a, "POST", srv.URL+"/api/v1/runs/"+run.RunID+"/share", nil, 201, &share)
	response, err := b.Get(srv.URL + "/api/v1/shares/" + share.Token)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatal("public share unavailable", err)
	}
	if strings.Contains(string(data), "SECRET_") || strings.Contains(string(data), owner) || strings.Contains(string(data), run.RunID) || strings.Contains(string(data), e.ExperimentID) {
		t.Fatal("private data leaked to public report")
	}
	var public map[string]json.RawMessage
	if err = json.Unmarshal(data, &public); err != nil {
		t.Fatal(err)
	}
	if len(public) != 7 {
		t.Fatalf("unexpected public fields: %v", public)
	}
	for _, key := range []string{"status", "source_type", "result_kind", "metrics", "stages", "slo", "slo_status"} {
		if _, ok := public[key]; !ok {
			t.Fatal("missing public field", key)
		}
	}
	for _, tc := range []struct{ method, path string }{{"GET", "/runs/" + run.RunID}, {"GET", "/runs/" + run.RunID + "/report"}, {"GET", "/runs/" + run.RunID + "/events"}, {"POST", "/runs/" + run.RunID + "/share"}, {"DELETE", "/runs/" + run.RunID + "/share"}, {"DELETE", "/runs/" + run.RunID}, {"DELETE", "/experiments/" + e.ExperimentID}, {"POST", "/runs/" + run.RunID + "/cancel"}, {"POST", "/experiments/" + e.ExperimentID + "/runner-tasks"}} {
		request(t, b, tc.method, srv.URL+"/api/v1"+tc.path, nil, 404, nil)
	}
	request(t, a, "DELETE", srv.URL+"/api/v1/runs/"+run.RunID+"/share", nil, 200, nil)
	request(t, b, "GET", srv.URL+"/api/v1/shares/"+share.Token, nil, 404, nil)
	var next struct{ Token string }
	request(t, a, "POST", srv.URL+"/api/v1/runs/"+run.RunID+"/share", nil, 201, &next)
	if next.Token == share.Token {
		t.Fatal("revoked token resurrected")
	}
	request(t, a, "DELETE", srv.URL+"/api/v1/experiments/"+e.ExperimentID, nil, 200, nil)
	request(t, b, "GET", srv.URL+"/api/v1/shares/"+next.Token, nil, 404, nil)
	for _, table := range []string{"runs", "experiments", "samples", "events", "report_shares"} {
		var n int
		if err = app.store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("associated rows retained", table, n, err)
		}
	}
	q, err := app.store.quota(owner)
	if err != nil || q.Reserved != 100 {
		t.Fatal("delete refunded quota", err)
	}
}
