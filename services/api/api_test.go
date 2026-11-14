package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{Name: "Mock release check", Target: Target{Type: "mock", Model: "mock-llm"}, Workload: Workload{ConcurrencySteps: []int{1, 3}, StageSeconds: 1, MaxRequests: 100, MaxRPS: 30, TimeoutMS: 500}, SLO: SLO{P95E2EMS: 200, ErrorRateMax: .1}}
}

func TestConfigValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Config)
	}{
		{"empty name", func(c *Config) { c.Name = " " }},
		{"real target", func(c *Config) { c.Target.Type = "openai_compatible" }},
		{"non increasing", func(c *Config) { c.Workload.ConcurrencySteps = []int{2, 1} }},
		{"unbounded concurrency", func(c *Config) { c.Workload.ConcurrencySteps = []int{33} }},
		{"unbounded rps", func(c *Config) { c.Workload.MaxRPS = 51 }},
		{"zero rps", func(c *Config) { c.Workload.MaxRPS = 0 }},
		{"unbounded requests", func(c *Config) { c.Workload.MaxRequests = 1001 }},
		{"unbounded duration", func(c *Config) { c.Workload.StageSeconds = 31 }},
		{"unbounded total", func(c *Config) { c.Workload.ConcurrencySteps = []int{1, 2, 3, 4, 5}; c.Workload.StageSeconds = 30 }},
		{"timeout", func(c *Config) { c.Workload.TimeoutMS = 0 }},
		{"invalid slo", func(c *Config) { c.SLO.ErrorRateMax = 1.1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testConfig()
			tc.change(&c)
			if c.validate() == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	c := testConfig()
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMetricsRawPercentile(t *testing.T) {
	samples := make([]Sample, 100)
	for i := range samples {
		samples[i].LatencyMS = float64(i + 1)
	}
	samples[0].ErrorCode = "REQUEST_TIMEOUT"
	m := metrics(samples, 100, 2*time.Second)
	if m.P95E2EMS != 95 || m.P50E2EMS != 50 || m.ErrorRate != .01 || m.ThroughputRPS != 50 || m.SuccessCount != 99 {
		t.Fatalf("unexpected metrics: %+v", m)
	}
	if sloStatus(metrics(samples[:1], 1, time.Second), testConfig().SLO) != "insufficient_data" {
		t.Fatal("small sample must be insufficient")
	}
}

func clientWithCookies() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 5 * time.Second}
}

func request(t *testing.T, client *http.Client, method, url string, body any, want int, target any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s got %d, want %d: %s", method, url, response.StatusCode, want, data)
	}
	if target != nil {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
}

func waitRun(t *testing.T, client *http.Client, base, id string) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var run Run
		request(t, client, "GET", base+"/api/v1/runs/"+id, nil, 200, &run)
		if run.terminal() {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run did not finish")
	return Run{}
}

func TestAPILifecyclePersistenceIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inferlab.db")
	app, err := newApp(path, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app)
	defer func() { server.Close(); app.Close() }()
	client := clientWithCookies()
	base := server.URL
	request(t, client, "GET", base+"/healthz", nil, 200, nil)
	headerResponse, err := client.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	headerResponse.Body.Close()
	for _, header := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy"} {
		if headerResponse.Header.Get(header) == "" {
			t.Fatalf("missing security header %s", header)
		}
	}
	var experiment Experiment
	config := testConfig()
	request(t, client, "POST", base+"/api/v1/experiments", config, 201, &experiment)
	var loaded Experiment
	request(t, client, "GET", base+"/api/v1/experiments/"+experiment.ExperimentID, nil, 200, &loaded)
	if loaded.Config.Name != config.Name {
		t.Fatal("configuration changed")
	}
	var started Run
	request(t, client, "POST", base+"/api/v1/experiments/"+experiment.ExperimentID+"/runs", nil, 202, &started)
	request(t, client, "POST", base+"/api/v1/experiments/"+experiment.ExperimentID+"/runs", nil, 429, nil)
	request(t, client, "GET", base+"/api/v1/runs/"+started.RunID+"/report", nil, 409, nil)
	stranger := clientWithCookies()
	request(t, stranger, "GET", base+"/api/v1/experiments/"+experiment.ExperimentID, nil, 404, nil)
	request(t, stranger, "GET", base+"/api/v1/runs/"+started.RunID, nil, 404, nil)
	request(t, stranger, "POST", base+"/api/v1/runs/"+started.RunID+"/cancel", nil, 404, nil)
	run := waitRun(t, client, base, started.RunID)
	if run.Status != "succeeded" || len(run.StageSummaries) != 2 || run.CompletedCount == 0 || run.CompletedCount != run.RequestCount || run.RequestCount > config.Workload.MaxRequests || run.ErrorCount+run.SuccessCount != run.CompletedCount || run.Config.Name != config.Name {
		t.Fatalf("invalid result: %+v", run)
	}
	var stageCount int
	for _, stage := range run.StageSummaries {
		stageCount += stage.CompletedCount
		if stage.RequestCount > config.Workload.StageSeconds*int(config.Workload.MaxRPS)+1 {
			t.Fatal("stage exceeded RPS budget")
		}
	}
	if stageCount != run.CompletedCount {
		t.Fatal("stage counts inconsistent")
	}
	var recent struct {
		Runs []Run `json:"runs"`
	}
	request(t, client, "GET", base+"/api/v1/runs", nil, 200, &recent)
	if len(recent.Runs) != 1 || recent.Runs[0].RunID != run.RunID {
		t.Fatalf("run history missing current run: %+v", recent.Runs)
	}
	var persistedSamples int
	if err := app.store.db.QueryRow("SELECT count(*) FROM samples WHERE run_id=?", run.RunID).Scan(&persistedSamples); err != nil || persistedSamples != run.CompletedCount {
		t.Fatalf("samples not persisted: count=%d err=%v", persistedSamples, err)
	}
	var report Report
	request(t, client, "GET", base+"/api/v1/runs/"+run.RunID+"/report", nil, 200, &report)
	if report.Methodology == "" || report.ResultKind != "demo" || len(report.Recommendations) == 0 {
		t.Fatal("incomplete report")
	}
	var events struct {
		Events []Event `json:"events"`
	}
	request(t, client, "GET", base+"/api/v1/runs/"+run.RunID+"/events", nil, 200, &events)
	if len(events.Events) < 6 {
		t.Fatalf("missing lifecycle events: %+v", events)
	}
	server.Close()
	app.Close()
	app, err = newApp(path, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	server = httptest.NewServer(app)
	// Keep the same browser cookie when the test server receives a new port.
	request(t, client, "GET", server.URL+"/api/v1/runs/"+run.RunID, nil, 200, &loadedRunForTest)
	if loadedRunForTest.RunID != run.RunID || loadedRunForTest.P95E2EMS != run.P95E2EMS {
		t.Fatal("restart lost metrics or session")
	}
}

var loadedRunForTest Run

func TestCancellationBudgetTimeoutAndInputSafety(t *testing.T) {
	app, err := newApp(filepath.Join(t.TempDir(), "db"), nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(app)
	defer server.Close()
	client := clientWithCookies()
	base := server.URL
	config := testConfig()
	config.Workload.StageSeconds = 10
	var experiment Experiment
	request(t, client, "POST", base+"/api/v1/experiments", config, 201, &experiment)
	var run Run
	request(t, client, "POST", base+"/api/v1/experiments/"+experiment.ExperimentID+"/runs", nil, 202, &run)
	time.Sleep(100 * time.Millisecond)
	request(t, client, "POST", base+"/api/v1/runs/"+run.RunID+"/cancel", nil, 202, nil)
	canceled := waitRun(t, client, base, run.RunID)
	if canceled.Status != "canceled" || canceled.ErrorCode != "RUN_CANCELED" || canceled.RequestCount != canceled.CompletedCount {
		t.Fatalf("cancellation inconsistent: %+v", canceled)
	}
	config.Workload.MaxRequests = 3
	config.Workload.TimeoutMS = 10
	request(t, client, "POST", base+"/api/v1/experiments", config, 201, &experiment)
	request(t, client, "POST", base+"/api/v1/experiments/"+experiment.ExperimentID+"/runs", nil, 202, &run)
	limited := waitRun(t, client, base, run.RunID)
	if limited.RequestCount != 3 || limited.ErrorCount != 3 || limited.CompletedCount != 3 {
		t.Fatalf("budget or timeout ignored: %+v", limited)
	}
	for _, body := range []string{`{"name":"secret","target":{"type":"mock","endpoint":"http://169.254.169.254"}}`, `{"api_key":"do-not-store"}`, strings.Repeat("x", 17000)} {
		req, _ := http.NewRequest("POST", base+"/api/v1/experiments", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 400 || strings.Contains(string(data), "do-not-store") {
			t.Fatalf("unsafe body handling: %d %s", resp.StatusCode, data)
		}
	}
	req, _ := http.NewRequest("POST", base+"/api/v1/experiments", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("cross-site request accepted")
	}
}

type observingAdapter struct {
	mu                sync.Mutex
	active, maxActive int
	starts            []time.Time
}

func (a *observingAdapter) Request(ctx context.Context) string {
	a.mu.Lock()
	a.active++
	if a.active > a.maxActive {
		a.maxActive = a.active
	}
	a.starts = append(a.starts, time.Now())
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.active--; a.mu.Unlock() }()
	select {
	case <-ctx.Done():
		return "REQUEST_TIMEOUT"
	case <-time.After(120 * time.Millisecond):
		return ""
	}
}

func TestRunnerConcurrencyAndRPS(t *testing.T) {
	store, err := openStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	adapter := &observingAdapter{}
	runner := newRunner(store, adapter)
	defer runner.Close()
	config := testConfig()
	config.Workload.ConcurrencySteps = []int{2}
	config.Workload.MaxRPS = 50
	config.Workload.MaxRequests = 10
	experiment := Experiment{ExperimentID: "test", Config: config}
	if err := store.createExperiment("owner", experiment); err != nil {
		t.Fatal(err)
	}
	run, err := runner.start("owner", experiment)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		run, err = store.run("owner", run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.terminal() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runner stuck")
		}
		time.Sleep(5 * time.Millisecond)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.maxActive != 2 {
		t.Fatalf("unexpected max concurrency %d", adapter.maxActive)
	}
	for i := 1; i < len(adapter.starts); i++ {
		if adapter.starts[i].Sub(adapter.starts[i-1]) < 19*time.Millisecond {
			t.Fatal("requests violate global RPS spacing")
		}
	}
	if run.RequestCount != 10 {
		t.Fatal("request cap not honored")
	}
}

func TestRestartRecoveryAndProductionSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	store, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig()
	experiment := Experiment{ExperimentID: "exp", Config: config}
	if err := store.createExperiment("owner", experiment); err != nil {
		t.Fatal(err)
	}
	run := Run{RunID: "run", ExperimentID: "exp", Status: "running", Config: config, StageSummaries: []StageSummary{}}
	if err := store.createRun("owner", run); err != nil {
		t.Fatal(err)
	}
	store.db.Close()
	app, err := newApp(path, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	recovered, err := app.store.run("owner", "run")
	if err != nil || recovered.Status != "failed" || recovered.ErrorCode != "RUN_INTERRUPTED" || recovered.FinishedAt == nil {
		t.Fatalf("bad recovery %+v %v", recovered, err)
	}
	if _, err := newApp(filepath.Join(t.TempDir(), "prod"), nil, true, ""); err == nil {
		t.Fatal("production must require explicit secret")
	}
}

func TestSnapshotAndMetricsConsistency(t *testing.T) {
	store, err := openStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	config := testConfig()
	experiment := Experiment{ExperimentID: "snapshot", Config: config}
	if err := store.createExperiment("owner", experiment); err != nil {
		t.Fatal(err)
	}
	runner := newRunner(store, &observingAdapter{})
	defer runner.Close()
	run, err := runner.start("owner", experiment)
	if err != nil {
		t.Fatal(err)
	}
	experiment.Name = "changed"
	data, _ := json.Marshal(experiment)
	if _, err := store.db.Exec("UPDATE experiments SET data=? WHERE id=?", data, experiment.ExperimentID); err != nil {
		t.Fatal(err)
	}
	saved, err := store.run("owner", run.RunID)
	if err != nil || saved.Config.Name != config.Name {
		t.Fatal("run snapshot changed with experiment")
	}
	m := metrics([]Sample{{LatencyMS: 4}, {LatencyMS: 8, ErrorCode: "HTTP_503"}}, 2, time.Second)
	if math.Abs(m.ErrorRate-.5) > .001 {
		t.Fatal(fmt.Sprint(m))
	}
}
