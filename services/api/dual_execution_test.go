package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type dualFixture struct {
	app         *App
	api, target *httptest.Server
	options     runnerCLIOptions
	owner       string
	experiment  Experiment
	calls       atomic.Int64
}

func newDualFixture(t *testing.T, handler http.HandlerFunc) *dualFixture {
	t.Helper()
	t.Setenv("PLATFORM_EXECUTION_ENABLED", "true")
	t.Setenv("PLATFORM_WORKER_TOKEN", strings.Repeat("test-worker-", 4))
	t.Setenv("PLATFORM_LOCATION", "test-lab")
	t.Setenv("PLATFORM_ISOLATION_CONFIRMED", "true")
	f := &dualFixture{owner: newID("visitor")}
	f.target = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		io.Copy(io.Discard, r.Body)
		if r.Header.Get("Authorization") != "Bearer dual-fake-secret" || r.Header.Get("X-InferLab-Runner-Token") != "" || r.Header.Get("X-InferLab-Worker-Token") != "" {
			t.Error("target received wrong credential scope")
		}
		if handler != nil {
			handler(w, r)
			return
		}
		io.WriteString(w, `{"choices":[{}]}`)
	}))
	var err error
	f.app, err = newApp(":memory:", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	f.api = httptest.NewTLSServer(f.app)
	roots := x509.NewCertPool()
	roots.AddCert(f.target.Certificate())
	roots.AddCert(f.api.Certificate())
	tc := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	f.options = runnerCLIOptions{targetPolicy: EndpointPolicy{AllowLoopback: true, TLSConfig: tc}, apiTLSConfig: tc}
	f.app.executions.policy = f.options.targetPolicy
	c := testConfig()
	c.Target = Target{Type: "openai_compatible", Endpoint: f.target.URL + "/v1", Model: "test-model"}
	c.Workload = Workload{ConcurrencySteps: []int{1}, StageSeconds: 1, MaxRequests: 2, MaxRPS: 5, TimeoutMS: 1000}
	f.experiment = Experiment{ExperimentID: newID("exp"), Config: c}
	if err := f.app.store.createExperiment(f.owner, f.experiment); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/api/v1/auth/me", nil, f.owner)
	t.Cleanup(func() { f.api.Close(); f.app.Close(); f.target.Close() })
	return f
}
func (f *dualFixture) request(t *testing.T, method, path string, input any, owner string) (int, []byte) {
	t.Helper()
	var body io.Reader
	if input != nil {
		data, _ := json.Marshal(input)
		body = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, f.api.URL+path, body)
	req.Header.Set("Content-Type", "application/json")
	if owner != "" {
		req.AddCookie(&http.Cookie{Name: "inferlab_session", Value: owner + "." + f.app.sign(owner)})
	}
	res, err := f.api.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}
func (f *dualFixture) create(t *testing.T, mode string) (Run, string) {
	t.Helper()
	status, data := f.request(t, "POST", "/api/v1/experiments/"+f.experiment.ExperimentID+"/executions", map[string]any{"mode": mode, "authorized": true, "api_key": "dual-fake-secret", "location": "test-local"}, f.owner)
	if status != 202 {
		t.Fatalf("create returned %d %s", status, data)
	}
	var out struct {
		Run  Run    `json:"run"`
		Code string `json:"claim_code"`
	}
	if json.Unmarshal(data, &out) != nil {
		t.Fatal("decode")
	}
	if bytes.Contains(data, []byte("dual-fake-secret")) || bytes.Contains(data, []byte("task_token")) {
		t.Fatal("create leaked credential")
	}
	return out.Run, out.Code
}
func (f *dualFixture) startWorker(t *testing.T) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); runWorker(ctx, []string{"--api-url", f.api.URL}, io.Discard, f.options) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.app.executions.mu.Lock()
		online := !f.app.executions.workerSeen.IsZero()
		f.app.executions.mu.Unlock()
		if online {
			return cancel
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker not online")
	return cancel
}
func (f *dualFixture) wait(t *testing.T, id string) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := f.app.store.run(f.owner, id)
		if err != nil {
			t.Fatal(err)
		}
		if run.terminal() {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run did not finish")
	return Run{}
}
func TestDualExecutionTLSRoundTrip(t *testing.T) {
	for _, mode := range []string{"platform", "local_agent"} {
		t.Run(mode, func(t *testing.T) {
			f := newDualFixture(t, nil)
			if mode == "platform" {
				f.startWorker(t)
			}
			run, code := f.create(t, mode)
			if mode == "local_agent" {
				var out bytes.Buffer
				data, _ := json.Marshal(map[string]string{"run_id": run.RunID, "code": code})
				result := runAgentCLI(context.Background(), []string{"--api-url", f.api.URL, "--authorize-target"}, bytes.NewReader(data), &out, f.options)
				if result != 0 {
					t.Fatalf("agent=%d %s", result, out.String())
				}
				if strings.Contains(out.String(), code) || strings.Contains(out.String(), "dual-fake-secret") {
					t.Fatal("sensitive client output")
				}
			}
			result := f.wait(t, run.RunID)
			if result.Status != "succeeded" || result.SuccessCount != 2 || f.calls.Load() != 2 || result.Execution.Mode != mode {
				t.Fatalf("bad E2E result %+v", result)
			}
			for i := 0; i < 2; i++ {
				status, data := f.request(t, "GET", "/api/v1/runs/"+run.RunID+"/report", nil, f.owner)
				if status != 200 || !bytes.Contains(data, []byte("stage_summaries")) || !bytes.Contains(data, []byte("execution")) || bytes.Contains(data, []byte("dual-fake-secret")) || bytes.Contains(data, []byte(code+"private-marker")) {
					t.Fatal("report failed")
				}
			}
			rows, err := f.app.store.db.Query("SELECT data FROM runs UNION ALL SELECT data FROM experiments UNION ALL SELECT data FROM events")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var data []byte
				rows.Scan(&data)
				if bytes.Contains(data, []byte("dual-fake-secret")) || code != "" && bytes.Contains(data, []byte(code)) {
					t.Fatal("DB contains credential")
				}
			}
		})
	}
}
func TestDualExecutionCancellationAbortsInFlight(t *testing.T) {
	for _, mode := range []string{"platform", "local_agent"} {
		t.Run(mode, func(t *testing.T) {
			entered, aborted := make(chan struct{}, 4), make(chan struct{}, 4)
			f := newDualFixture(t, func(w http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				<-r.Context().Done()
				aborted <- struct{}{}
			})
			if mode == "platform" {
				f.startWorker(t)
			}
			run, code := f.create(t, mode)
			done := make(chan struct{})
			if mode == "local_agent" {
				go func() {
					defer close(done)
					data, _ := json.Marshal(map[string]string{"run_id": run.RunID, "code": code})
					runAgentCLI(context.Background(), []string{"--api-url", f.api.URL, "--authorize-target"}, bytes.NewReader(data), io.Discard, f.options)
				}()
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("target never entered")
			}
			status, _ := f.request(t, "POST", "/api/v1/runs/"+run.RunID+"/cancel", nil, f.owner)
			if status != 202 {
				t.Fatal(status)
			}
			select {
			case <-aborted:
			case <-time.After(2 * time.Second):
				t.Fatal("in-flight request not aborted")
			}
			count := f.calls.Load()
			time.Sleep(400 * time.Millisecond)
			if f.calls.Load() != count {
				t.Fatal("sent requests after cancellation")
			}
			if f.wait(t, run.RunID).Status != "canceled" {
				t.Fatal("late result overwrote cancel")
			}
			if mode == "local_agent" {
				<-done
			}
		})
	}
}
func TestDualExecutionClaimBoundaries(t *testing.T) {
	f := newDualFixture(t, nil)
	run, code := f.create(t, "local_agent")
	body := map[string]string{"run_id": run.RunID, "code": code}
	status, _ := f.request(t, "POST", "/api/v1/runner-claims", body, newID("visitor"))
	if status != 403 {
		t.Fatal("cross-user claim allowed")
	}
	status, _ = f.request(t, "POST", "/api/v1/runner-claims", map[string]string{"run_id": run.RunID, "code": "claim_wrong"}, "")
	if status != 403 {
		t.Fatal("wrong code allowed")
	}
	status, data := f.request(t, "POST", "/api/v1/runner-claims", body, "")
	if status != 200 {
		t.Fatal(status)
	}
	var claim executionClaim
	json.Unmarshal(data, &claim)
	status, _ = f.request(t, "POST", "/api/v1/runner-claims", body, "")
	if status != 403 {
		t.Fatal("replay allowed")
	}
	// Owner session and legacy task token cannot bypass the new lease.
	result := validLocalResult(run.RunID, run.Config)
	status, _ = f.request(t, "POST", "/api/v1/runner-tasks/"+run.RunID+"/result", result, f.owner)
	if status != 403 {
		t.Fatal("legacy result bypass allowed")
	}
	f.request(t, "POST", "/api/v1/runs/"+run.RunID+"/cancel", nil, f.owner)
	origin, _ := url.Parse(f.api.URL)
	status, _ = executionRequest(context.Background(), origin, claim.Task.ResultEndpoint, "POST", claim.Task.TaskToken, "", result, nil, f.options.apiTLSConfig)
	if status != 409 {
		t.Fatal("late lease result allowed")
	}
	f.experiment.ExperimentID = newID("exp")
	if err := f.app.store.createExperiment(f.owner, f.experiment); err != nil {
		t.Fatal(err)
	}
	run, code = f.create(t, "local_agent")
	f.app.executions.mu.Lock()
	f.app.executions.entries[run.RunID].expires = time.Now().Add(-time.Second)
	f.app.executions.mu.Unlock()
	status, _ = f.request(t, "POST", "/api/v1/runner-claims", map[string]string{"run_id": run.RunID, "code": code}, "")
	if status != 403 || f.wait(t, run.RunID).ErrorCode != "EXECUTION_EXPIRED" {
		t.Fatal("expired code allowed")
	}
	for i := 0; i < 12; i++ {
		status, _ = f.request(t, "POST", "/api/v1/runner-claims", body, "")
	}
	if status != 429 {
		t.Fatal("claim rate limit absent")
	}
	if f.calls.Load() != 0 {
		t.Fatal("claim rejection sent target requests")
	}
}
func TestDualExecutionAtomicAdmissionAndClaim(t *testing.T) {
	f := newDualFixture(t, nil)
	var wg sync.WaitGroup
	var accepted atomic.Int64
	var winningRun Run
	var winningCode string
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, data := f.request(t, "POST", "/api/v1/experiments/"+f.experiment.ExperimentID+"/executions", map[string]any{"mode": "local_agent", "authorized": true, "api_key": "dual-fake-secret"}, f.owner)
			if status == 202 {
				accepted.Add(1)
				var out struct {
					Run  Run    `json:"run"`
					Code string `json:"claim_code"`
				}
				json.Unmarshal(data, &out)
				mu.Lock()
				winningRun, winningCode = out.Run, out.Code
				mu.Unlock()
			} else if status != 429 && status != 409 {
				t.Errorf("unexpected %d", status)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("admitted duplicate")
	}
	quota, err := f.app.store.quota(f.owner)
	if err != nil || quota.Reserved != 2 {
		t.Fatal("quota not atomic")
	}
	accepted.Store(0)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _ := f.request(t, "POST", "/api/v1/runner-claims", map[string]string{"run_id": winningRun.RunID, "code": winningCode}, "")
			if status == 200 {
				accepted.Add(1)
			} else if status != 403 {
				t.Errorf("unexpected claim %d", status)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("duplicate claim")
	}
}
func TestDualExecutionWorkerLossNeverRequeues(t *testing.T) {
	f := newDualFixture(t, nil)
	f.app.executions.mu.Lock()
	f.app.executions.workerSeen = time.Now()
	f.app.executions.mu.Unlock()
	run, _ := f.create(t, "platform")
	origin, _ := url.Parse(f.api.URL)
	var claim executionClaim
	status, err := executionRequest(context.Background(), origin, "/internal/worker/poll", "POST", "", strings.Repeat("test-worker-", 4), map[string]string{"instance": newID("worker")}, &claim, f.options.apiTLSConfig)
	if status != 200 || err != nil {
		t.Fatal("claim")
	}
	f.app.executions.mu.Lock()
	f.app.executions.entries[run.RunID].seen = time.Now().Add(-4 * time.Second)
	f.app.executions.mu.Unlock()
	status, err = executionRequest(context.Background(), origin, "/internal/worker/poll", "POST", "", strings.Repeat("test-worker-", 4), map[string]string{"instance": newID("worker")}, nil, f.options.apiTLSConfig)
	if status != 204 || err != nil {
		t.Fatal("worker restart received old task")
	}
	if f.wait(t, run.RunID).ErrorCode != "EXECUTION_LEASE_LOST" || f.calls.Load() != 0 {
		t.Fatal("lost task re-executed")
	}
	status, _ = executionRequest(context.Background(), origin, "/internal/worker/poll", "POST", "", "wrong", map[string]string{"instance": newID("worker")}, nil, f.options.apiTLSConfig)
	if status != 403 {
		t.Fatal("worker authentication absent")
	}
}
func TestDualExecutionAuthorizationLimitsAndSource(t *testing.T) {
	f := newDualFixture(t, nil)
	path := "/api/v1/experiments/" + f.experiment.ExperimentID + "/executions"
	for _, input := range []map[string]any{{"mode": "local_agent"}, {"mode": "invalid", "authorized": true}} {
		status, _ := f.request(t, "POST", path, input, f.owner)
		if status != 400 {
			t.Fatal("invalid authorization accepted")
		}
	}
	status, _ := f.request(t, "POST", path, map[string]any{"mode": "platform", "authorized": true, "api_key": "dual-fake-secret"}, f.owner)
	if status != 503 {
		t.Fatal("offline platform accepted")
	}
	c := f.experiment.Config
	c.Workload.MaxRequests = 101
	if validateExecutionConfig(context.Background(), c, f.options.targetPolicy) == nil {
		t.Fatal("budget accepted")
	}
	if normalizedTarget("https://EXAMPLE.com:443/v1/") != normalizedTarget("https://example.com/v1") {
		t.Fatal("target normalization")
	}
	a := Run{Status: "succeeded", SourceType: "openai_compatible", Execution: &ExecutionSnapshot{Mode: "platform", Location: "A", Trust: "platform_worker"}}
	b := a
	b.Execution = &ExecutionSnapshot{Mode: "local_agent", Location: "A", Trust: "client_reported"}
	if code, _ := comparisonIssue(a, b); code != "EXECUTION_SOURCE_MISMATCH" {
		t.Fatal("mixed measurement trusted")
	}
	b.Execution = &ExecutionSnapshot{Mode: "platform", Location: "B", Trust: "platform_worker"}
	if code, _ := comparisonIssue(a, b); code != "EXECUTION_SOURCE_MISMATCH" {
		t.Fatal("different locations trusted")
	}
}

func TestDualExecutionSlowResultCannotBlockCancel(t *testing.T) {
	f := newDualFixture(t, nil)
	run, code := f.create(t, "local_agent")
	_, data := f.request(t, "POST", "/api/v1/runner-claims", map[string]string{"run_id": run.RunID, "code": code}, "")
	var claim executionClaim
	json.Unmarshal(data, &claim)
	resultData, _ := json.Marshal(validLocalResult(run.RunID, run.Config))
	body := &gatedRunnerBody{Reader: bytes.NewReader(resultData), entered: make(chan struct{}), release: make(chan struct{})}
	request := httptest.NewRequest("POST", claim.Task.ResultEndpoint, body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-InferLab-Runner-Token", claim.Task.TaskToken)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); f.app.ServeHTTP(recorder, request) }()
	<-body.entered
	canceled := make(chan struct{})
	go func() { defer close(canceled); f.request(t, "POST", "/api/v1/runs/"+run.RunID+"/cancel", nil, f.owner) }()
	blocked := false
	select {
	case <-canceled:
	case <-time.After(200 * time.Millisecond):
		blocked = true
	}
	close(body.release)
	<-canceled
	<-done
	if blocked {
		t.Fatal("slow result body blocked cancellation")
	}
	if f.wait(t, run.RunID).Status != "canceled" || recorder.Code != 409 {
		t.Fatal("late result overwrote cancel")
	}
}

func TestDualExecutionSSRFAndRedirectTraps(t *testing.T) {
	var trapCalls atomic.Int64
	trap := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { trapCalls.Add(1) }))
	defer trap.Close()
	for _, raw := range []string{trap.URL + "/v1", "https://10.0.0.1/v1", "https://169.254.169.254/v1", "https://100.100.100.200/v1", "https://[::1]/v1", "https://198.18.0.1/v1", "https://[64:ff9b::a00:1]/v1", "https://[2002:7f00:1::]/v1"} {
		if _, err := newOpenAICompatibleAdapter(Target{Type: "openai_compatible", Endpoint: raw, Model: "test"}, "fake-secret"); err == nil {
			t.Fatal("SSRF target accepted")
		}
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, trap.URL+"/v1/chat/completions", 307)
	}))
	defer redirect.Close()
	roots := x509.NewCertPool()
	roots.AddCert(redirect.Certificate())
	adapter, err := newOpenAICompatibleAdapterWithPolicy(Target{Type: "openai_compatible", Endpoint: redirect.URL + "/v1", Model: "test"}, "fake-secret", EndpointPolicy{AllowLoopback: true, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}})
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if adapter.Request(context.Background()) != ErrRedirectBlocked || trapCalls.Load() != 0 {
		t.Fatal("redirect trap received request/key")
	}
}

func TestDualExecutionMixedDNSAndRebinding(t *testing.T) {
	mixed := EndpointPolicy{Resolve: func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.1"), net.ParseIP("127.0.0.1")}, nil
	}}
	if _, err := validateEndpoint(context.Background(), "https://controlled.invalid/v1", mixed); err == nil {
		t.Fatal("mixed DNS accepted on submission")
	}
	if conn, err := safeDialWithPolicy(mixed)(context.Background(), "tcp", "controlled.invalid:443"); err == nil || conn != nil || !strings.Contains(err.Error(), ErrSSRFBlocked) {
		t.Fatal("mixed DNS accepted on connect")
	}
	var lookups atomic.Int64
	rebind := EndpointPolicy{Resolve: func(context.Context, string, string) ([]net.IP, error) {
		if lookups.Add(1) == 1 {
			return []net.IP{net.ParseIP("203.0.113.1")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}}
	adapter, err := newOpenAICompatibleAdapterWithPolicy(Target{Type: "openai_compatible", Endpoint: "https://controlled.invalid/v1", Model: "test"}, "fake-secret", rebind)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if adapter.Request(context.Background()) != ErrSSRFBlocked {
		t.Fatal("DNS rebinding accepted")
	}
}

func TestDualExecutionAPIRecoveryRevokesPrivateClaim(t *testing.T) {
	app, err := newApp(t.TempDir()+"/recovery.db", nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	var dbPath string
	if err := app.store.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&dbPath); err != nil {
		t.Fatal(err)
	}
	owner := newID("visitor")
	config := testConfig()
	config.Target = Target{Type: "openai_compatible", Endpoint: "https://203.0.113.1/v1", Model: "fixture"}
	config.Workload = Workload{ConcurrencySteps: []int{1}, StageSeconds: 1, MaxRequests: 2, MaxRPS: 2, TimeoutMS: 1000}
	experiment := Experiment{ExperimentID: newID("exp"), Config: config}
	if err := app.store.createExperiment(owner, experiment); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/api/v1/experiments/"+experiment.ExperimentID+"/executions", strings.NewReader(`{"mode":"local_agent","authorized":true,"api_key":"crash-test-private-key"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "inferlab_session", Value: owner + "." + app.sign(owner)})
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)
	if recorder.Code != 202 {
		t.Fatalf("create %d", recorder.Code)
	}
	var created struct {
		Run  Run    `json:"run"`
		Code string `json:"claim_code"`
	}
	json.Unmarshal(recorder.Body.Bytes(), &created)
	// Stop timers without writing terminal states, as if the API process disappeared.
	close(app.executions.stop)
	<-app.executions.done
	app.runner.Close()
	app.credentials.close()
	app.mock.Close()
	app.store.db.Close()
	restarted, err := newApp(dbPath, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	run, err := restarted.store.run(owner, created.Run.RunID)
	if err != nil || run.Status != "failed" || run.ErrorCode != "RUN_INTERRUPTED" {
		t.Fatal("restart silently resumed run")
	}
	body, _ := json.Marshal(map[string]string{"run_id": run.RunID, "code": created.Code})
	request = httptest.NewRequest("POST", "/api/v1/runner-claims", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	restarted.ServeHTTP(recorder, request)
	if recorder.Code != 403 {
		t.Fatal("claim survived restart")
	}
}

func TestDualExecutionTargetAndPlatformCapacity(t *testing.T) {
	f := newDualFixture(t, nil)
	f.app.executions.mu.Lock()
	f.app.executions.workerSeen = time.Now()
	f.app.executions.mu.Unlock()
	run, _ := f.create(t, "platform")
	other := newID("visitor")
	second := f.experiment
	second.ExperimentID = newID("exp")
	second.Target.Endpoint += "/"
	if err := f.app.store.createExperiment(other, second); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"mode": "local_agent", "authorized": true, "api_key": "dual-fake-secret"}
	status, _ := f.request(t, "POST", "/api/v1/experiments/"+second.ExperimentID+"/executions", input, other)
	if status != 429 {
		t.Fatal("same normalized target admitted")
	}
	second.ExperimentID = newID("exp")
	second.Target.Endpoint = f.target.URL + "/different"
	if err := f.app.store.createExperiment(other, second); err != nil {
		t.Fatal(err)
	}
	input["mode"] = "platform"
	status, _ = f.request(t, "POST", "/api/v1/experiments/"+second.ExperimentID+"/executions", input, other)
	if status != 429 {
		t.Fatal("second platform task admitted")
	}
	f.request(t, "POST", "/api/v1/runs/"+run.RunID+"/cancel", nil, f.owner)
	status, _ = f.request(t, "POST", "/api/v1/experiments/"+f.experiment.ExperimentID+"/executions", input, f.owner)
	if status != 409 {
		t.Fatal("repeated submission after terminal was not deduplicated")
	}
}

func TestDualExecutionExpiryClearsSecretsWithUnavailableStorage(t *testing.T) {
	f := newDualFixture(t, nil)
	run, _ := f.create(t, "local_agent")
	f.app.executions.mu.Lock()
	defer f.app.executions.mu.Unlock()
	f.app.executions.entries[run.RunID].expires = time.Now().Add(-time.Second)
	f.app.store.db.Close()
	f.app.expireExecutionsLocked(time.Now())
	if len(f.app.executions.entries) != 0 {
		t.Fatal("expired credential retained after database failure")
	}
}
