package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicStatusAndProtectedMetrics(t *testing.T) {
	t.Setenv("OPS_METRICS_TOKEN", "fixture-scrape")
	t.Setenv("PUBLIC_SUPPORT_URL", "https://support.example.test/help")
	app, err := newApp(":memory:", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	check := func(path, token string, want int) *httptest.ResponseRecorder {
		t.Helper()
		res := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		app.ServeHTTP(res, req)
		if res.Code != want || res.Header().Get("Set-Cookie") != "" || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: unexpected HTTP result (%d)", path, res.Code)
		}
		return res
	}
	check("/internal/ops/metrics", "", 404)
	check("/internal/ops/metrics", "wrong", 404)
	res := check("/api/v1/status", "", 200)
	var status map[string]any
	if json.Unmarshal(res.Body.Bytes(), &status) != nil || len(status) != 3 || status["status"] != "operational" || status["checked_at"] == nil || status["support_url"] != "https://support.example.test/help" {
		t.Fatal("public data did not match allowlist")
	}
	e := Experiment{ExperimentID: "private-fixture", Config: testConfig()}
	if err := app.store.createExperiment("fixture-owner", e); err != nil {
		t.Fatal(err)
	}
	run := Run{RunID: "run-fixture", ExperimentID: e.ExperimentID, Status: "queued", Config: e.Config}
	if err := app.store.createRun("fixture-owner", run); err != nil {
		t.Fatal(err)
	}
	run.Status = "failed"
	if err := app.store.finishLocalRun("fixture-owner", run, "run_failed"); err != nil {
		t.Fatal(err)
	}
	if app.store.finishLocalRun("fixture-owner", run, "run_failed") != errRunAlreadyFinished {
		t.Fatal("duplicate completion accepted")
	}
	if err := app.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	res = check("/api/v1/status", "", 200)
	if !strings.Contains(res.Body.String(), `"status":"degraded"`) {
		t.Fatal("failed check not reflected in public status")
	}
	check("/api/v1/healthz", "", 503)
	var metrics map[string]uint64
	res = check("/internal/ops/metrics", "fixture-scrape", 200)
	if json.Unmarshal(res.Body.Bytes(), &metrics) != nil || len(metrics) != 3 || metrics["http_5xx"] != 1 || metrics["task_failures"] != 1 || metrics["payment_failures"] != 0 {
		t.Fatal("aggregate counts did not match actual failures")
	}
}
