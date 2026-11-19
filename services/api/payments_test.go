package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type paymentTransport func(*http.Request) (*http.Response, error)

func (f paymentTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReportExportWatchaContract(t *testing.T) {
	t.Setenv("PAYMENTS_ENABLED", "false")
	t.Setenv("INVITE_CODE", "test-invite-fixture")
	app, err := newApp(":memory:", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	var mode atomic.Int32
	var calls atomic.Int32
	var redirected atomic.Int32
	const sameBuyer = "buyer ID must be stable"
	buyerIDs := make(chan string, 20)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			redirected.Add(1)
			return
		}
		calls.Add(1)
		var body map[string]string
		if r.URL.Path != "/v1/entitlements/access" || r.Method != "POST" ||
			r.Header.Get("Authorization") != "Bearer fixture-only" || r.Header.Get("Content-Type") != "application/json" ||
			json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 2 || body["entitlement_id"] != "fixture-entitlement" || body["user_id"] == "" {
			t.Error("unexpected provider request")
			w.WriteHeader(400)
			return
		}
		buyerIDs <- body["user_id"]
		switch mode.Load() {
		case 0:
			fmt.Fprint(w, `{"access":"purchase_required","entitlement":{"type":"permanent"},"purchase":{"url":"alipays://platformapi/startapp?appId=fixture"}}`)
		case 1:
			fmt.Fprint(w, `{"access":"granted","entitlement":{"type":"permanent"}}`)
		case 2:
			http.Redirect(w, r, "/redirected", 307)
		case 3:
			<-r.Context().Done()
		case 4:
			fmt.Fprint(w, `{"access":"granted","entitlement":{"type":"quota"}}`)
		case 5:
			fmt.Fprint(w, `{"access":"unavailable","reason":{"message":"private provider data"}}`)
		case 6:
			fmt.Fprint(w, `{"access":"granted","entitlement":{"type":"permanent"}} {}`)
		case 7:
			fmt.Fprint(w, `{"access":"purchase_required","entitlement":{"type":"permanent"},"purchase":{"url":"javascript:alert(1)"}}`)
		}
	}))
	defer provider.Close()
	fakeURL, err := url.Parse(provider.URL)
	if err != nil {
		t.Fatal(err)
	}
	access := newWatcha("fixture-only", "fixture-entitlement", paymentTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != watchaEndpoint {
			t.Error("provider base changed")
		}
		copy := r.Clone(r.Context())
		copy.URL.Scheme, copy.URL.Host = fakeURL.Scheme, fakeURL.Host
		return http.DefaultTransport.RoundTrip(copy)
	}), 250*time.Millisecond)

	// Drive real session cookies and both public handlers without changing live configuration.
	for _, name := range []string{"alice", "bob"} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/auth/register", strings.NewReader(`{"username":"`+name+`","password":"fixture-password","invite":"test-invite-fixture","claim_anonymous":false}`))
		r.Header.Set("Content-Type", "application/json")
		app.ServeHTTP(rec, r)
		if rec.Code != 201 {
			t.Fatalf("register: %d", rec.Code)
		}
	}
	var owner string
	if err := app.store.db.QueryRow("SELECT id FROM users WHERE username='alice'").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	e := Experiment{ExperimentID: "exp-fixture", Config: testConfig()}
	e.Config.Name, e.Config.Target.Endpoint = "=fixture-formula", "https://private.invalid/path"
	if err := app.store.createExperiment(owner, e); err != nil {
		t.Fatal(err)
	}
	run := Run{RunID: "run-fixture", ExperimentID: e.ExperimentID, Status: "queued", Config: e.Config, Metrics: Metrics{RequestCount: 7}, StageSummaries: []StageSummary{{Stage: 1, Concurrency: 2}}}
	if err := app.store.createRun(owner, run); err != nil {
		t.Fatal(err)
	}
	run.Status = "succeeded"
	if err := app.store.saveRun(run, nil, "run_succeeded"); err != nil {
		t.Fatal(err)
	}
	login := func(name string) *http.Cookie {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"`+name+`","password":"fixture-password"}`))
		r.Header.Set("Content-Type", "application/json")
		app.ServeHTTP(rec, r)
		if rec.Code != 200 || len(rec.Result().Cookies()) == 0 {
			t.Fatalf("login: %d", rec.Code)
		}
		return rec.Result().Cookies()[0]
	}
	alice, bob := login("alice"), login("bob")
	get := func(cookie *http.Cookie, path string, want int) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/v1/runs/"+path, nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		app.ServeHTTP(rec, r)
		if rec.Code != want || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: status %d, want %d", path, rec.Code, want)
		}
		if strings.Contains(rec.Body.String(), "private provider data") {
			t.Fatal("provider details leaked")
		}
		return rec
	}
	const check = "run-fixture/report/export-access"
	const export = "run-fixture/report.csv"
	get(alice, check, 404)
	if calls.Load() != 0 {
		t.Fatal("disabled request reached the provider")
	}
	app.payments = access
	get(bob, check, 404)
	get(bob, export, 404)
	get(nil, check, 401)
	get(alice, "missing/report.csv", 404)
	if calls.Load() != 0 {
		t.Fatal("unauthorized request reached the provider")
	}
	if res := get(alice, check, 200); !strings.Contains(res.Body.String(), "alipays://platformapi/startapp") {
		t.Fatal("missing purchase URL")
	}
	get(alice, export+"?access=granted", 402)
	mode.Store(1)
	alice = login("alice")
	if res := get(alice, check, 200); !strings.Contains(res.Body.String(), "granted") {
		t.Fatal("purchase did not unlock access")
	}
	res := get(alice, export, 200)
	csvBody := res.Body.String()
	records, err := csv.NewReader(strings.NewReader(csvBody)).ReadAll()
	if err != nil || len(records) != 3 || records[1][3] != "7" || strings.Contains(csvBody, "fixture") || res.Header().Get("Content-Disposition") == "" {
		t.Fatal("unexpected CSV")
	}
	for range 4 {
		select {
		case id := <-buyerIDs:
			if id != owner {
				t.Fatal(sameBuyer)
			}
		default:
			t.Fatal("missing provider request")
		}
	}
	for _, failure := range []int32{2, 3, 4, 5, 6} {
		mode.Store(failure)
		get(alice, export, 503)
	}
	mode.Store(7)
	if res := get(alice, check, 200); strings.Contains(res.Body.String(), "purchase_url") {
		t.Fatal("unsafe link returned")
	}
	if redirected.Load() != 0 || app.ops.paymentFailures.Load() != 5 {
		t.Fatal("redirect followed or payment failure counters incorrect")
	}
}

func TestWatchaEnvironmentSeparation(t *testing.T) {
	t.Setenv("PAYMENTS_ENABLED", "true")
	t.Setenv("WATCHA_SANDBOX_API_KEY", "sandbox-fixture")
	t.Setenv("WATCHA_SANDBOX_ENTITLEMENT_ID", "sandbox")
	t.Setenv("WATCHA_LIVE_API_KEY", "live-fixture")
	t.Setenv("WATCHA_LIVE_ENTITLEMENT_ID", "live")
	for env, want := range map[string]string{"staging": "sandbox", "production": "live"} {
		t.Setenv("APP_ENV", env)
		client, err := configuredWatcha()
		if err != nil || client == nil || client.entitlement != want || client.key != want+"-fixture" {
			t.Fatal("wrong environment credentials")
		}
	}
	t.Setenv("APP_ENV", "local")
	if client, err := configuredWatcha(); err == nil || client != nil {
		t.Fatal("local accepted real payment configuration")
	}
}
