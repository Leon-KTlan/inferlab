package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Adapter interface{ Request(context.Context) string }

type MockAdapter struct {
	server   *http.Server
	listener net.Listener
	client   *http.Client
}

func newMockAdapter() (*MockAdapter, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	var active atomic.Int64
	var sequence atomic.Uint64
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var delay time.Duration
		var fail bool
		if scenario := r.Header.Get(mockScenarioHeader); scenario != "" {
			var ok bool
			delay, ok = mockScenarioDelay(scenario)
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		} else {
			n := active.Add(1)
			defer active.Add(-1)
			seq := sequence.Add(1)
			delay = time.Duration(35+n*15+int64(seq%7)*5) * time.Millisecond
			fail = seq%17 == 0
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"mock-llm","choices":[{"finish_reason":"stop"}]}`)
	})}
	transport := &http.Transport{Proxy: nil, MaxIdleConns: 64, MaxIdleConnsPerHost: 32, IdleConnTimeout: 30 * time.Second}
	m := &MockAdapter{server: server, listener: listener, client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	go server.Serve(listener)
	return m, nil
}

func (m *MockAdapter) Request(ctx context.Context) string {
	return m.requestWithScenario(ctx, "")
}

func (m *MockAdapter) requestWithScenario(ctx context.Context, scenario string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+m.listener.Addr().String()+"/v1/chat/completions", nil)
	if err != nil {
		return "REQUEST_INVALID"
	}
	if scenario != "" {
		req.Header.Set(mockScenarioHeader, scenario)
	}
	response, err := m.client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "REQUEST_TIMEOUT"
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return "REQUEST_CANCELED"
		}
		return "MOCK_CONNECTION_FAILED"
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
		return "RESPONSE_READ_FAILED"
	}
	if response.StatusCode != http.StatusOK {
		return "HTTP_" + strconv.Itoa(response.StatusCode)
	}
	return ""
}

func (m *MockAdapter) Close() { m.server.Close(); m.client.CloseIdleConnections() }

type activeRun struct {
	owner  string
	cancel context.CancelFunc
}
type Runner struct {
	store   *Store
	adapter Adapter
	mu      sync.Mutex
	active  map[string]activeRun
	wg      sync.WaitGroup
	closed  bool
}

var errBusy = errors.New("run quota exceeded")
var errLocalRunnerRequired = errors.New("openai-compatible targets require a local runner")

func newRunner(store *Store, adapter Adapter) *Runner {
	return &Runner{store: store, adapter: adapter, active: make(map[string]activeRun)}
}

func (r *Runner) start(owner string, experiment Experiment) (Run, error) {
	if experiment.Target.Type != "mock" {
		return Run{}, errLocalRunnerRequired
	}
	return r.startWithContext(context.Background(), owner, experiment, "mock", "demo")
}

func (r *Runner) startWithContext(parent context.Context, owner string, experiment Experiment, sourceType, resultKind string, runIDs ...string) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || len(r.active) >= 4 {
		return Run{}, errBusy
	}
	for _, active := range r.active {
		if active.owner == owner {
			return Run{}, errBusy
		}
	}
	runID := newID("run")
	if len(runIDs) > 0 && runIDs[0] != "" {
		runID = runIDs[0]
	}
	softwareVersion := "0.1.0"
	if sourceType == "mock" {
		softwareVersion = "0.1.1"
	}
	run := Run{RunID: runID, ExperimentID: experiment.ExperimentID, Status: "queued", SourceType: sourceType, ResultKind: resultKind, SoftwareVersion: softwareVersion, Config: experiment.Config, CreatedAt: time.Now().UTC(), StageSummaries: []StageSummary{}}
	if err := r.store.createRun(owner, run); err != nil {
		return Run{}, err
	}
	ctx, cancel := context.WithCancel(parent)
	r.active[run.RunID] = activeRun{owner: owner, cancel: cancel}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer cancel()
		defer func() { r.mu.Lock(); delete(r.active, run.RunID); r.mu.Unlock() }()
		r.execute(ctx, run)
	}()
	return run, nil
}

func (r *Runner) cancel(owner, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if active, ok := r.active[id]; ok {
		current, err := r.store.runOwner(id)
		if err == nil && current == owner {
			active.cancel()
		}
	}
}
func (r *Runner) Close() {
	r.mu.Lock()
	r.closed = true
	for _, a := range r.active {
		a.cancel()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

func (r *Runner) execute(ctx context.Context, run Run) {
	start := time.Now().UTC()
	run.StartedAt = &start
	run.Status = "running"
	finish := func(status, code string) {
		now := time.Now().UTC()
		run.Status = status
		run.ErrorCode = code
		run.FinishedAt = &now
		r.store.saveRun(run, nil, "run_"+status)
	}
	if err := r.store.saveRun(run, nil, "run_started"); err != nil {
		finish("failed", "STORAGE_WRITE_FAILED")
		return
	}
	request := r.adapter.Request
	if mock, ok := r.adapter.(*MockAdapter); ok && run.Config.Target.MockScenario != "" {
		scenario := run.Config.Target.MockScenario
		request = func(ctx context.Context) string { return mock.requestWithScenario(ctx, scenario) }
	}
	var allSamples []Sample
	nextDispatch := time.Now()
	for index, concurrency := range run.Config.Workload.ConcurrencySteps {
		if ctx.Err() != nil || run.RequestCount >= run.Config.Workload.MaxRequests {
			break
		}
		run.CurrentStage = index + 1
		stageStart := time.Now()
		stageCtx, stageCancel := context.WithTimeout(ctx, time.Duration(run.Config.Workload.StageSeconds)*time.Second)
		requestParent, cancelRequests := stageCtx, stageCancel
		if run.SourceType == "mock" {
			// The send window closes before outstanding Mock requests finish.
			requestParent, cancelRequests = context.WithCancel(ctx)
		}
		stageSamples := []Sample{}
		stageSent := 0
		inflight := 0
		stopping := false
		storageFailed := false
		run.StageSummaries = append(run.StageSummaries, StageSummary{Stage: index + 1, Concurrency: concurrency, SLOStatus: "insufficient_data"})
		if err := r.store.saveRun(run, nil, "stage_started"); err != nil {
			stageCancel()
			cancelRequests()
			finish("failed", "STORAGE_WRITE_FAILED")
			return
		}
		completed := make(chan Sample, concurrency)
		interval := time.Duration(float64(time.Second) / run.Config.Workload.MaxRPS)
		timer := time.NewTimer(0)
		for {
			if stopping && inflight == 0 {
				break
			}
			select {
			case <-stageCtx.Done():
				stopping = true
			case <-timer.C:
				if stageCtx.Err() == nil && !stopping && inflight < concurrency && run.RequestCount < run.Config.Workload.MaxRequests && !time.Now().Before(nextDispatch) {
					inflight++
					stageSent++
					run.RequestCount++
					nextDispatch = time.Now().Add(interval)
					go func(stage int) {
						began := time.Now().UTC()
						requestCtx, cancel := context.WithTimeout(requestParent, time.Duration(run.Config.Workload.TimeoutMS)*time.Millisecond)
						code := request(requestCtx)
						cancel()
						completed <- Sample{Stage: stage, StartedAt: began, LatencyMS: float64(time.Since(began)) / float64(time.Millisecond), ErrorCode: code}
					}(index + 1)
				}
				if run.RequestCount >= run.Config.Workload.MaxRequests {
					stopping = true
				}
				delay := time.Until(nextDispatch)
				if delay < time.Millisecond {
					delay = time.Millisecond
				}
				timer.Reset(delay)
			case sample := <-completed:
				inflight--
				stageSamples = append(stageSamples, sample)
				allSamples = append(allSamples, sample)
				run.Metrics = metrics(allSamples, run.RequestCount, time.Since(start))
				summary := &run.StageSummaries[len(run.StageSummaries)-1]
				summary.Metrics = metrics(stageSamples, stageSent, time.Since(stageStart))
				summary.SLOStatus = sloStatus(summary.Metrics, run.Config.SLO)
				if err := r.store.saveRun(run, &sample, ""); err != nil {
					storageFailed = true
					stopping = true
					cancelRequests()
				}
			}
			if stopping && inflight > 0 {
				// Wait for in-flight requests without repeatedly selecting the closed send window.
				for inflight > 0 {
					sample := <-completed
					inflight--
					stageSamples = append(stageSamples, sample)
					allSamples = append(allSamples, sample)
					run.Metrics = metrics(allSamples, run.RequestCount, time.Since(start))
					summary := &run.StageSummaries[len(run.StageSummaries)-1]
					summary.Metrics = metrics(stageSamples, stageSent, time.Since(stageStart))
					summary.SLOStatus = sloStatus(summary.Metrics, run.Config.SLO)
					if err := r.store.saveRun(run, &sample, ""); err != nil {
						storageFailed = true
						cancelRequests()
					}
				}
			}
		}
		timer.Stop()
		stageCancel()
		cancelRequests()
		summary := &run.StageSummaries[len(run.StageSummaries)-1]
		summary.Metrics = metrics(stageSamples, stageSent, time.Since(stageStart))
		summary.SLOStatus = sloStatus(summary.Metrics, run.Config.SLO)
		run.Metrics = metrics(allSamples, run.RequestCount, time.Since(start))
		if storageFailed {
			finish("failed", "STORAGE_WRITE_FAILED")
			return
		}
		if err := r.store.saveRun(run, nil, "stage_finished"); err != nil {
			finish("failed", "STORAGE_WRITE_FAILED")
			return
		}
	}
	if ctx.Err() != nil {
		finish("canceled", "RUN_CANCELED")
		return
	}
	finish("succeeded", "")
}
