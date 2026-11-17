package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type cliFixture struct {
	app         *App
	api         *httptest.Server
	target      *httptest.Server
	task        RunnerTask
	path        string
	options     runnerCLIOptions
	owner       string
	targetCalls atomic.Int64
}

func newCLIFixture(t *testing.T, targetHandler http.HandlerFunc) *cliFixture {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "runner-test-secret-key")
	f := &cliFixture{owner: newID("visitor")}
	f.target = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.targetCalls.Add(1)
		var payload struct {
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.MaxTokens != 32 {
			t.Error("probe request omitted the bounded output setting")
		}
		if r.Header.Get("Authorization") != "Bearer runner-test-secret-key" || r.Header.Get("X-InferLab-Runner-Token") != "" || r.URL.Path != "/v1/chat/completions" {
			t.Error("target received incorrect authorization, task credential, or path")
		}
		if targetHandler != nil {
			targetHandler(w, r)
			return
		}
		io.WriteString(w, `{"choices":[{"finish_reason":"stop"}]}`)
	}))
	app, err := newApp(":memory:", nil, false, "")
	if err != nil {
		f.target.Close()
		t.Fatal(err)
	}
	f.app = app
	f.api = httptest.NewTLSServer(app)
	t.Cleanup(func() { f.api.Close(); f.app.Close(); f.target.Close() })
	roots := x509.NewCertPool()
	roots.AddCert(f.api.Certificate())
	roots.AddCert(f.target.Certificate())
	f.options = runnerCLIOptions{targetPolicy: EndpointPolicy{AllowLoopback: true, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, apiTLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	config := testConfig()
	config.Target = Target{Type: "openai_compatible", Endpoint: f.target.URL + "/v1", Model: "local-test-model"}
	config.Workload.ConcurrencySteps = []int{1}
	config.Workload.MaxRequests = 2
	config.Workload.MaxRPS = 20
	// Only the fixture seeds a loopback target; production task creation and CLI stay strict.
	experiment := Experiment{ExperimentID: newID("exp"), Config: config}
	if err := app.store.createExperiment(f.owner, experiment); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", f.api.URL+"/api/v1/experiments/"+experiment.ExperimentID+"/runner-tasks", nil)
	req.AddCookie(&http.Cookie{Name: "inferlab_session", Value: f.owner + "." + app.sign(f.owner)})
	response, err := f.api.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 202 {
		t.Fatalf("task download returned %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&f.task); err != nil {
		t.Fatal(err)
	}
	f.path = filepath.Join(t.TempDir(), "task.json")
	f.saveTask(t)
	return f
}

func (f *cliFixture) saveTask(t *testing.T) {
	t.Helper()
	data, err := json.Marshal(f.task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *cliFixture) run(t *testing.T, ctx context.Context, apiURL string, authorize bool) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	args := []string{"--task", f.path, "--api-url", apiURL}
	if authorize {
		args = append(args, "--authorize-target")
	}
	code := runRunnerCLIWithOptions(ctx, args, &out, &errOut, f.options)
	for _, secret := range []string{"runner-test-secret-key", f.task.TaskToken, "health check"} {
		if strings.Contains(out.String()+errOut.String(), secret) {
			t.Fatal("CLI output leaked sensitive data")
		}
	}
	return code, out.String(), errOut.String()
}

func TestRunnerCLIDownloadExecuteSubmit(t *testing.T) {
	f := newCLIFixture(t, nil)
	code, out, errOut := f.run(t, context.Background(), f.api.URL, true)
	if code != 0 || !strings.Contains(out, "accepted: succeeded") || errOut != "" {
		t.Fatalf("CLI returned %d %s %s", code, out, errOut)
	}
	run, err := f.app.store.run(f.owner, f.task.RunID)
	if err != nil || run.Status != "succeeded" || run.CompletedCount != 2 || run.SuccessCount != 2 || f.targetCalls.Load() != 2 {
		t.Fatalf("result not persisted: status=%s count=%d err=%v", run.Status, run.CompletedCount, err)
	}
	req, _ := http.NewRequest("GET", f.api.URL+"/api/v1/runs/"+run.RunID+"/report", nil)
	req.AddCookie(&http.Cookie{Name: "inferlab_session", Value: f.owner + "." + f.app.sign(f.owner)})
	response, err := f.api.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var report Report
	if err := json.NewDecoder(response.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.Note, "本地 Runner") || strings.Contains(report.Note, "Mock 实测") {
		t.Fatal("real report mislabeled as Mock")
	}
}

func TestRunnerCLIRequiresAuthorization(t *testing.T) {
	f := newCLIFixture(t, nil)
	code, _, errOut := f.run(t, context.Background(), f.api.URL, false)
	if code != 2 || !strings.Contains(errOut, "TARGET_AUTH_REQUIRED") || f.targetCalls.Load() != 0 {
		t.Fatal("unauthorized CLI issued a target request")
	}
	var out, errOutput bytes.Buffer
	if code := runRunnerCLI(context.Background(), []string{"--task", f.path, "--api-url", f.api.URL, "--authorize-target=false"}, &out, &errOutput); code != 2 {
		t.Fatal("false authorization accepted")
	}
}

func TestRunnerCLIErrorsAreSubmitted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{{"auth", 401, ErrAuthFailed}, {"rate", 429, ErrRateLimited}, {"server", 503, ErrHTTP5xx}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCLIFixture(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "private upstream response", tc.status) })
			code, _, errOut := f.run(t, context.Background(), f.api.URL, true)
			if code != 1 || !strings.Contains(errOut, tc.want) || strings.Contains(errOut, "private upstream") {
				t.Fatalf("error classification incorrect: %d %s", code, errOut)
			}
			run, err := f.app.store.run(f.owner, f.task.RunID)
			if err != nil || run.Status != "failed" || run.ErrorCode != tc.want || run.ErrorCount != 2 {
				t.Fatalf("error not persisted: status=%s code=%s err=%v", run.Status, run.ErrorCode, err)
			}
		})
	}
	t.Run("missing key", func(t *testing.T) {
		f := newCLIFixture(t, nil)
		t.Setenv("OPENAI_API_KEY", "")
		code, _, _ := f.run(t, context.Background(), f.api.URL, true)
		run, err := f.app.store.run(f.owner, f.task.RunID)
		if code != 1 || err != nil || run.Status != "failed" || run.ErrorCode != "TARGET_AUTH_REQUIRED" || f.targetCalls.Load() != 0 {
			t.Fatal("missing key did not produce a safe failed result")
		}
	})
}

func TestRunnerCLICancelAndTimeout(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f := newCLIFixture(t, func(w http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() })
		code, _, _ := f.run(t, ctx, f.api.URL, true)
		run, err := f.app.store.run(f.owner, f.task.RunID)
		if code != 130 || err != nil || run.Status != "canceled" || run.ErrorCode != "RUN_CANCELED" || f.targetCalls.Load() != 1 {
			t.Fatalf("cancel did not stop and persist: exit=%d status=%s code=%s err=%v", code, run.Status, run.ErrorCode, err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		f := newCLIFixture(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		f.task.Config.Workload.TimeoutMS = 10
		f.saveTask(t)
		code, _, errOut := f.run(t, context.Background(), f.api.URL, true)
		if code != 1 || !strings.Contains(errOut, ErrTargetTimeout) {
			t.Fatalf("timeout was not classified: %d %s", code, errOut)
		}
		run, err := f.app.store.run(f.owner, f.task.RunID)
		if err != nil || run.ErrorCode != ErrTargetTimeout || run.ErrorCount != 2 {
			t.Fatal("timeout results missing")
		}
	})
}

func TestRunnerCLISubmissionIsBoundToTrustedAPI(t *testing.T) {
	f := newCLIFixture(t, nil)
	var leaks atomic.Int64
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaks.Add(1) }))
	defer trap.Close()
	original := f.task.ResultEndpoint
	for _, endpoint := range []string{trap.URL + original, "//evil.example" + original, "/api/v1/runner-tasks/other/result", original + "?token=secret", original + "#fragment", "/api/v1/runner-tasks/../result"} {
		f.task.ResultEndpoint = endpoint
		f.saveTask(t)
		code, _, _ := f.run(t, context.Background(), f.api.URL, true)
		if code != 2 {
			t.Fatalf("malicious result endpoint accepted: exit %d", code)
		}
	}
	if f.targetCalls.Load() != 0 || leaks.Load() != 0 {
		t.Fatal("invalid task triggered a request")
	}
	f.task.ResultEndpoint = original
	f.saveTask(t)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("API received target key")
		}
		http.Redirect(w, r, trap.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	code, _, errOut := f.run(t, context.Background(), redirect.URL, true)
	if code != 1 || !strings.Contains(errOut, "RUNNER_RESULT_SUBMISSION_FAILED") || leaks.Load() != 0 {
		t.Fatal("result redirect followed or falsely acknowledged")
	}
}

func TestRunnerCLIRejectsUnsafeTasksAndAPIOrigins(t *testing.T) {
	f := newCLIFixture(t, nil)
	for _, raw := range []string{"http://example.com", "http://169.254.169.254", "https://user:password@example.com", "https://example.com/path", "https://example.com?token=secret", "https://example.com#fragment", "https://example.com:0", "https://example.com:65536"} {
		code, _, _ := f.run(t, context.Background(), raw, true)
		if code != 2 {
			t.Fatalf("unsafe API origin accepted: exit=%d", code)
		}
	}
	original := f.task
	for _, change := range []func(*RunnerTask){func(task *RunnerTask) { task.TaskID = "run_different" }, func(task *RunnerTask) { task.RunID = "../run" }, func(task *RunnerTask) { task.TaskToken = "bad\r\nheader" }, func(task *RunnerTask) { task.Config.Workload.MaxRequests = 1001 }, func(task *RunnerTask) { task.Config.Target.Type = "mock" }, func(task *RunnerTask) { task.Config.Target.Endpoint = "https://169.254.169.254/v1" }} {
		f.task = original
		change(&f.task)
		f.saveTask(t)
		code, _, _ := f.run(t, context.Background(), f.api.URL, true)
		if code != 2 {
			t.Fatalf("unsafe task accepted: exit=%d", code)
		}
	}
	f.task = original
	f.saveTask(t)
	var out, errOut bytes.Buffer
	if code := runRunnerCLI(context.Background(), []string{"--task", f.path, "--api-url", f.api.URL, "--authorize-target"}, &out, &errOut); code != 2 {
		t.Fatal("production CLI allowed loopback target")
	}
	if f.targetCalls.Load() != 0 {
		t.Fatal("unsafe configuration sent target requests")
	}
}

func TestRunnerCLITLSVerificationAndAcknowledgment(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		trust   bool
	}{
		{"untrusted certificate", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 202, map[string]string{"run_id": "unused", "status": "succeeded"})
		}, false},
		{"wrong acknowledgment", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 202, map[string]string{"run_id": "other", "status": "succeeded"})
		}, true},
		{"rejected", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCLIFixture(t, nil)
			api := httptest.NewTLSServer(tc.handler)
			defer api.Close()
			if !tc.trust {
				f.options.apiTLSConfig = nil
			}
			code, _, errOut := f.run(t, context.Background(), api.URL, true)
			if code != 1 || !strings.Contains(errOut, "RUNNER_RESULT_SUBMISSION_FAILED") {
				t.Fatal("unconfirmed submission returned success")
			}
		})
	}
}

func validLocalResult(runID string, config Config) RunnerResult {
	started := time.Now().UTC().Add(-time.Duration(len(config.Workload.ConcurrencySteps)) * time.Second)
	finished := time.Now().UTC()
	r := RunnerResult{RunID: runID, Status: "succeeded", StartedAt: &started, FinishedAt: &finished, CurrentStage: len(config.Workload.ConcurrencySteps)}
	var all []Sample
	for index, step := range config.Workload.ConcurrencySteps {
		samples := []Sample{{Stage: index + 1, LatencyMS: 5}}
		m := metrics(samples, 1, time.Second)
		r.StageSummaries = append(r.StageSummaries, StageSummary{Stage: index + 1, Concurrency: step, Metrics: m, SLOStatus: sloStatus(m, config.SLO)})
		all = append(all, samples...)
	}
	r.Metrics = metrics(all, len(all), time.Duration(len(config.Workload.ConcurrencySteps))*time.Second)
	return r
}

func TestRunnerResultConsistencyValidation(t *testing.T) {
	config := testConfig()
	base := validLocalResult("run_test", config)
	if err := base.validateFor(config); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*RunnerResult)
	}{
		{"negative percentile", func(r *RunnerResult) { r.Metrics.P95E2EMS = -1 }},
		{"impossible latency", func(r *RunnerResult) { r.Metrics.P95E2EMS = 1e9 }},
		{"nonfinite", func(r *RunnerResult) { r.Metrics.P95E2EMS = math.Inf(1) }},
		{"nan", func(r *RunnerResult) { r.StageSummaries[0].ErrorRate = math.NaN() }},
		{"incorrect error rate", func(r *RunnerResult) { r.Metrics.ErrorRate = .5 }},
		{"incorrect throughput", func(r *RunnerResult) { r.Metrics.ThroughputRPS = 100 }},
		{"stage counts disagree", func(r *RunnerResult) { r.StageSummaries[0].RequestCount++ }},
		{"extra stage", func(r *RunnerResult) { r.StageSummaries = append(r.StageSummaries, r.StageSummaries[0]) }},
		{"wrong concurrency", func(r *RunnerResult) { r.StageSummaries[0].Concurrency++ }},
		{"wrong stage index", func(r *RunnerResult) { r.StageSummaries[0].Stage++ }},
		{"untrusted slo", func(r *RunnerResult) { r.StageSummaries[0].SLOStatus = "pass" }},
		{"missing timestamp", func(r *RunnerResult) { r.StartedAt = nil }},
		{"reversed time", func(r *RunnerResult) { now := r.StartedAt.Add(-time.Second); r.FinishedAt = &now }},
		{"window too short", func(r *RunnerResult) { now := r.StartedAt.Add(time.Millisecond); r.FinishedAt = &now }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			r.StageSummaries = append([]StageSummary(nil), base.StageSummaries...)
			tc.change(&r)
			if err := r.validateFor(config); err == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
	for _, status := range []string{"failed", "canceled"} {
		r := base
		r.Status = status
		r.ErrorCode = ErrConnectionFailed
		if status == "canceled" {
			r.ErrorCode = "RUN_CANCELED"
		}
		r.StageSummaries = r.StageSummaries[:1]
		r.CurrentStage = 1
		r.Metrics = r.StageSummaries[0].Metrics
		if err := r.validateFor(config); err != nil {
			t.Fatalf("partial %s rejected: %v", status, err)
		}
	}
}
