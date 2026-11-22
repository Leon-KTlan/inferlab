package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const intelligenceCLISecret = "intelligence-fixture-only-api-key"
const intelligenceCLIPrivateResponse = "private fixture upstream response"

type intelligenceCLIFixture struct {
	app         *App
	api         *httptest.Server
	target      *httptest.Server
	task        RunnerTask
	path        string
	owner       string
	options     runnerCLIOptions
	targetCalls atomic.Int64
	activeCalls atomic.Int64
	mu          sync.Mutex
	requestAt   []time.Time
}

func newIntelligenceCLIFixture(t *testing.T, temporaryCredential bool) *intelligenceCLIFixture {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", intelligenceCLISecret)
	f := &intelligenceCLIFixture{owner: newID("visitor")}
	f.target = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.targetCalls.Add(1)
		if f.activeCalls.Add(1) != 1 {
			t.Error("intelligence requests exceeded single concurrency")
		}
		defer f.activeCalls.Add(-1)
		f.mu.Lock()
		f.requestAt = append(f.requestAt, time.Now())
		f.mu.Unlock()
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+intelligenceCLISecret || r.Header.Get("X-InferLab-Runner-Token") != "" {
			t.Error("target received an invalid method, path, or credential")
		}
		var payload struct {
			Model       string   `json:"model"`
			MaxTokens   int      `json:"max_tokens"`
			Temperature *float64 `json:"temperature"`
			Messages    []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Model != "fixture-intelligence-model" || payload.MaxTokens != 512 || payload.Temperature == nil || *payload.Temperature != 0 || len(payload.Messages) != 1 || payload.Messages[0].Role != "user" {
			t.Error("target request omitted bounded deterministic generation settings")
			http.Error(w, intelligenceCLIPrivateResponse, 400)
			return
		}
		for _, probe := range intelligenceProbes() {
			if probe.Prompt != payload.Messages[0].Content {
				continue
			}
			answer := probe.Expected
			switch probe.ID {
			case "reasoning-1":
				answer = "The answer is " + answer
			case "structured-1":
				answer = `{"color":"teal","count":3,"count":3}`
			case "context-2":
				http.Error(w, intelligenceCLIPrivateResponse, 503)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}, "finish_reason": "stop"}}})
			return
		}
		t.Error("target received a prompt outside the fixed suite")
		http.Error(w, intelligenceCLIPrivateResponse, 400)
	}))
	app, err := newApp(":memory:", nil, false, "")
	if err != nil {
		f.target.Close()
		t.Fatal(err)
	}
	f.app = app
	f.api = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("InferLab API received the target API key")
		}
		app.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { f.api.Close(); app.Close(); f.target.Close() })
	roots := x509.NewCertPool()
	roots.AddCert(f.api.Certificate())
	roots.AddCert(f.target.Certificate())
	trustedTLS := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	f.options = runnerCLIOptions{targetPolicy: EndpointPolicy{AllowLoopback: true, TLSConfig: trustedTLS}, apiTLSConfig: trustedTLS}
	config := testConfig()
	config.Target = Target{Type: "openai_compatible", Endpoint: f.target.URL + "/v1", Model: "fixture-intelligence-model"}
	config.Workload = Workload{ConcurrencySteps: []int{1}, StageSeconds: 30, MaxRequests: 10, MaxRPS: 1, TimeoutMS: 10000}
	config.Intelligence = &IntelligenceSettings{SuiteVersion: intelligenceSuiteVersion, Repetitions: 1}
	experiment := Experiment{ExperimentID: newID("exp"), Config: config, CreatedAt: time.Now().UTC()}
	if err := app.store.createExperiment(f.owner, experiment); err != nil {
		t.Fatal(err)
	}
	key := ""
	if temporaryCredential {
		key = intelligenceCLISecret
	}
	// Only this fixture seeds a loopback snapshot; public creation and the CLI remain strict.
	recorder := httptest.NewRecorder()
	app.startIntelligence(recorder, f.owner, experiment, key, false)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("fixture task creation returned %d", recorder.Code)
	}
	var created struct {
		Task RunnerTask `json:"task"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	f.task = created.Task
	f.path = filepath.Join(t.TempDir(), "intelligence-task.json")
	f.saveTask(t, f.task)
	return f
}

func (f *intelligenceCLIFixture) saveTask(t *testing.T, task RunnerTask) {
	t.Helper()
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), intelligenceCLISecret) {
		t.Fatal("task file contains the target API key")
	}
	if err := os.WriteFile(f.path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *intelligenceCLIFixture) run(t *testing.T, ctx context.Context, authorize bool) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args := []string{"--task", f.path, "--api-url", f.api.URL}
	if authorize {
		args = append(args, "--authorize-target")
	}
	code := runIntelligenceCLIWithOptions(ctx, args, &stdout, &stderr, f.options)
	output := stdout.String() + stderr.String()
	for _, private := range []string{intelligenceCLISecret, f.task.TaskToken, intelligenceCLIPrivateResponse, "The answer is CAB", `{"color":"teal"`} {
		if strings.Contains(output, private) {
			t.Fatal("CLI output leaked credentials or an upstream answer")
		}
	}
	for _, probe := range intelligenceProbes() {
		if strings.Contains(output, probe.Prompt) || len(probe.Expected) >= 3 && strings.Contains(output, probe.Expected) {
			t.Fatal("CLI output leaked a prompt or answer")
		}
	}
	return code, stdout.String(), stderr.String()
}

func TestIntelligenceCLITLSExecuteGradeAndSubmit(t *testing.T) {
	f := newIntelligenceCLIFixture(t, true)
	t.Setenv("OPENAI_API_KEY", "wrong-environment-key")
	code, stdout, stderr := f.run(t, context.Background(), true)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "accepted: succeeded (10/10 responses)") {
		t.Fatalf("CLI failed: exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	run, err := f.app.store.run(f.owner, f.task.RunID)
	if err != nil {
		t.Fatal(err)
	}
	report := run.IntelligenceReport
	if f.targetCalls.Load() != 10 || run.Status != "succeeded" || report == nil || report.Score != 70 || report.Completed != 10 || report.Passed != 7 || report.ErrorCount != 1 || !report.Complete {
		t.Fatalf("unexpected persisted result: status=%s report=%+v calls=%d", run.Status, report, f.targetCalls.Load())
	}
	seen := make(map[string]bool)
	for _, outcome := range report.Outcomes {
		if seen[outcome.ProbeID] || outcome.Repetition != 1 || outcome.LatencyMS < 0 {
			t.Fatal("runner failed to preserve unique per-probe outcomes")
		}
		seen[outcome.ProbeID] = true
		if outcome.ProbeID == "context-2" && (outcome.ErrorCode != ErrHTTP5xx || outcome.Passed) {
			t.Fatal("upstream error was not classified")
		}
		if (outcome.ProbeID == "reasoning-1" || outcome.ProbeID == "structured-1") && (outcome.Passed || outcome.ErrorCode != "") {
			t.Fatal("nonconforming answer was not distinguished from transport failure")
		}
	}
	f.mu.Lock()
	elapsed := f.requestAt[9].Sub(f.requestAt[0])
	f.mu.Unlock()
	if elapsed < 8800*time.Millisecond {
		t.Fatalf("10 requests bypassed the 1 req/s limit: %s", elapsed)
	}
	if _, available := f.app.credentials.take(f.task.RunID); available {
		t.Fatal("temporary credential survived its one-time claim")
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{intelligenceCLISecret, intelligenceCLIPrivateResponse, "The answer is CAB", `"prompt":`, `"content":`} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("persisted run contains a credential, prompt, or upstream response")
		}
	}
}

func TestIntelligenceCLIAuthorizationAndPreCanceledContext(t *testing.T) {
	f := newIntelligenceCLIFixture(t, false)
	if code, _, stderr := f.run(t, context.Background(), false); code != 2 || !strings.Contains(stderr, "TARGET_AUTH_REQUIRED") || f.targetCalls.Load() != 0 {
		t.Fatal("CLI contacted target without authorization")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, stdout, stderr := f.run(t, ctx, true)
	run, err := f.app.store.run(f.owner, f.task.RunID)
	if code != 130 || err != nil || run.Status != "canceled" || run.ErrorCode != "RUN_CANCELED" || f.targetCalls.Load() != 0 || !strings.Contains(stdout, "accepted: canceled") || stderr != "" {
		t.Fatalf("pre-cancellation was not submitted safely: exit=%d status=%s stderr=%s err=%v", code, run.Status, stderr, err)
	}
}

func TestIntelligenceCLIRejectsUnsafeTasks(t *testing.T) {
	f := newIntelligenceCLIFixture(t, false)
	for _, test := range []struct {
		name   string
		mutate func(*RunnerTask)
	}{
		{"cross run result", func(task *RunnerTask) { task.ResultEndpoint = "/api/v1/intelligence/runs/run_other/result" }},
		{"absolute result URL", func(task *RunnerTask) { task.ResultEndpoint = "https://evil.example" + task.ResultEndpoint }},
		{"performance result URL", func(task *RunnerTask) { task.ResultEndpoint = "/api/v1/runner-tasks/" + task.RunID + "/result" }},
		{"query in result URL", func(task *RunnerTask) { task.ResultEndpoint += "?secret=value" }},
		{"task identity", func(task *RunnerTask) { task.TaskID = "run_other" }},
		{"invalid token", func(task *RunnerTask) { task.TaskToken = "short-token" }},
		{"unsupported suite", func(task *RunnerTask) { task.Config.Intelligence.SuiteVersion = "future" }},
		{"too many rounds", func(task *RunnerTask) { task.Config.Intelligence.Repetitions = 4 }},
		{"mock profile on real target", func(task *RunnerTask) { task.Config.Intelligence.MockProfile = "reference" }},
		{"concurrency", func(task *RunnerTask) { task.Config.Workload.ConcurrencySteps = []int{2} }},
		{"request rate", func(task *RunnerTask) { task.Config.Workload.MaxRPS = 2 }},
		{"request count", func(task *RunnerTask) { task.Config.Workload.MaxRequests = 11 }},
		{"request timeout", func(task *RunnerTask) { task.Config.Workload.TimeoutMS = 10001 }},
		{"metadata SSRF", func(task *RunnerTask) { task.Config.Target.Endpoint = "https://169.254.169.254/v1" }},
		{"private SSRF", func(task *RunnerTask) { task.Config.Target.Endpoint = "https://10.0.0.1/v1" }},
		{"HTTP target", func(task *RunnerTask) { task.Config.Target.Endpoint = "http://203.0.113.1/v1" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, _ := json.Marshal(f.task)
			var task RunnerTask
			if err := json.Unmarshal(data, &task); err != nil {
				t.Fatal(err)
			}
			test.mutate(&task)
			f.saveTask(t, task)
			if code, _, stderr := f.run(t, context.Background(), true); code != 2 || !strings.Contains(stderr, "RUNNER_TASK_INVALID") {
				t.Fatalf("unsafe task accepted: exit=%d stderr=%s", code, stderr)
			}
		})
	}
	if f.targetCalls.Load() != 0 {
		t.Fatal("invalid task contacted a target")
	}
	if err := validateIntelligenceTask(context.Background(), f.task, EndpointPolicy{}); err == nil {
		t.Fatal("production policy accepted the fixture's loopback target")
	}
}

func TestIntelligenceCLIResultAcknowledgmentBinding(t *testing.T) {
	var redirected atomic.Int64
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer trap.Close()
	now := time.Now().UTC()
	result := IntelligenceRunnerResult{RunID: "run_fixture", Status: "canceled", Outcomes: []IntelligenceOutcome{}, StartedAt: &now, FinishedAt: &now, ErrorCode: "RUN_CANCELED"}
	for _, test := range []struct {
		name   string
		status int
		body   string
		wantOK bool
	}{
		{"matching", 202, `{"run_id":"run_fixture","status":"canceled"}`, true},
		{"other run", 202, `{"run_id":"run_other","status":"canceled"}`, false},
		{"other status", 202, `{"run_id":"run_fixture","status":"succeeded"}`, false},
		{"wrong HTTP status", 200, `{"run_id":"run_fixture","status":"canceled"}`, false},
		{"malformed", 202, `not JSON`, false},
		{"redirect", 307, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("X-InferLab-Runner-Token") != "fixture-task-token" || r.Header.Get("Authorization") != "" {
					t.Error("submission credential was not bound to the API request")
				}
				var submitted IntelligenceRunnerResult
				if json.NewDecoder(r.Body).Decode(&submitted) != nil || submitted.RunID != result.RunID || submitted.Status != result.Status {
					t.Error("submission did not contain the canceled task's result")
				}
				w.Header().Set("Location", trap.URL)
				w.WriteHeader(test.status)
				io.WriteString(w, test.body)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := submitIntelligenceResult(ctx, server.URL, "fixture-task-token", result, runnerCLIOptions{})
			if (err == nil) != test.wantOK {
				t.Fatalf("submission acknowledgment accepted=%t, want %t", err == nil, test.wantOK)
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("submission followed a redirect and leaked task data")
	}
}
