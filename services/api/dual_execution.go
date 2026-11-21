package main

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var errExecutionSubmitted = errors.New("execution already submitted for experiment")

// ExecutionSnapshot describes where observations were made, not provider location.
type ExecutionSnapshot struct {
	Mode     string `json:"mode"`
	Location string `json:"location"`
	Trust    string `json:"trust"`
}

type executionEntry struct {
	owner, codeHash, lease, key string
	task                        RunnerTask
	expires, deadline, seen     time.Time
	claimed                     bool
}
type executionRate struct {
	until time.Time
	count int
}
type executions struct {
	mu                    sync.Mutex
	entries               map[string]*executionEntry
	rates                 map[string]executionRate
	enabled               bool
	workerToken, location string
	workerSeen            time.Time
	policy                EndpointPolicy // Private dependency injection; no runtime bypass.
	stop, done            chan struct{}
}

func (a *App) initExecutions() error {
	d := &a.executions
	d.entries = make(map[string]*executionEntry)
	d.rates = make(map[string]executionRate)
	d.enabled = os.Getenv("PLATFORM_EXECUTION_ENABLED") == "true"
	d.workerToken, d.location = os.Getenv("PLATFORM_WORKER_TOKEN"), strings.TrimSpace(os.Getenv("PLATFORM_LOCATION"))
	if d.enabled && (len(d.workerToken) < 32 || d.location == "" || len(d.location) > 100 || os.Getenv("PLATFORM_ISOLATION_CONFIRMED") != "true") {
		return errors.New("platform requires worker token (32+ bytes), location and verified isolation confirmation")
	}
	d.stop, d.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(d.done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-d.stop:
				return
			case <-ticker.C:
				d.mu.Lock()
				a.expireExecutionsLocked(time.Now())
				d.mu.Unlock()
			}
		}
	}()
	return nil
}
func (a *App) closeExecutions() {
	close(a.executions.stop)
	<-a.executions.done
	a.executions.mu.Lock()
	defer a.executions.mu.Unlock()
	for id, e := range a.executions.entries {
		a.failExecutionLocked(id, e, "RUN_INTERRUPTED")
	}
}
func (a *App) failExecutionLocked(id string, e *executionEntry, code string) {
	e.key = ""
	e.lease = ""
	e.codeHash = ""
	delete(a.executions.entries, id)
	owner, err := a.store.runOwner(id)
	if err != nil {
		return
	}
	e.owner = owner
	run, err := a.store.run(e.owner, id)
	if err != nil {
		return
	}
	if !run.terminal() {
		now := time.Now().UTC()
		run.Status = "failed"
		run.ErrorCode = code
		run.FinishedAt = &now
		if a.store.finishLocalRun(e.owner, run, "execution_failed") != nil {
			return
		}
	}
}
func (a *App) expireExecutionsLocked(now time.Time) {
	for id, e := range a.executions.entries {
		if !e.claimed && !now.Before(e.expires) {
			a.failExecutionLocked(id, e, "EXECUTION_EXPIRED")
			continue
		}
		if e.claimed && (!now.Before(e.deadline) || now.Sub(e.seen) > 3*time.Second) {
			a.failExecutionLocked(id, e, "EXECUTION_LEASE_LOST")
			continue
		}
		owner, err := a.store.runOwner(id)
		if err != nil {
			continue
		}
		e.owner = owner
		run, err := a.store.run(e.owner, id)
		if err != nil {
			continue
		}
		if run.terminal() {
			e.key = ""
			delete(a.executions.entries, id)
			continue
		}
	}
}
func (a *App) executionCapabilities(w http.ResponseWriter, r *http.Request) {
	d := &a.executions
	d.mu.Lock()
	defer d.mu.Unlock()
	reason := ""
	if !d.enabled {
		reason = "平台代跑未配置，请选择本地 Agent。"
	} else if time.Since(d.workerSeen) > 3*time.Second {
		reason = "Worker 离线，请稍后重试或自行选择本地 Agent。"
	}
	writeJSON(w, 200, map[string]any{"platform_available": reason == "", "reason": reason, "location": d.location, "queue_ttl_seconds": 300})
}
func normalizedTarget(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if u.Port() != "" && u.Port() != "443" {
		host = net.JoinHostPort(host, u.Port())
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host + strings.TrimRight(u.EscapedPath(), "/")
}
func validateExecutionConfig(ctx context.Context, c Config, p EndpointPolicy) error {
	if err := validateLocalConfig(ctx, c, p); err != nil {
		return err
	}
	w := c.Workload
	if w.ConcurrencySteps[len(w.ConcurrencySteps)-1] > 4 || w.MaxRPS > 5 || w.MaxRequests > 100 || w.StageSeconds*len(w.ConcurrencySteps) > 45 {
		return errors.New("双模式上限：并发4、RPS5、100请求、累计阶段45秒、单请求10秒")
	}
	return nil
}
func (a *App) createExecution(w http.ResponseWriter, r *http.Request) {
	if !a.requireLogin(w, r) {
		return
	}
	owner := a.owner(w, r)
	experiment, err := a.store.experiment(owner, r.PathValue("id"))
	if err != nil {
		a.readError(w, err)
		return
	}
	var in struct {
		Mode       string `json:"mode"`
		Authorized bool   `json:"authorized"`
		APIKey     string `json:"api_key"`
		Location   string `json:"location"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !in.Authorized {
		writeError(w, 400, "TARGET_AUTH_REQUIRED", "请独立勾选目标测试授权。")
		return
	}
	if in.Mode != "platform" && in.Mode != "local_agent" {
		writeError(w, 400, "EXECUTION_MODE_INVALID", "请选择平台或本地 Agent。")
		return
	}
	if len(in.APIKey) > 4096 || strings.ContainsAny(in.APIKey, "\r\n") || (in.Mode == "platform" && strings.TrimSpace(in.APIKey) == "") {
		writeError(w, 400, "INVALID_API_KEY", "平台运行需要有效 API Key。")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := validateExecutionConfig(ctx, experiment.Config, a.executions.policy); err != nil {
		writeError(w, 400, "EXECUTION_CONFIG_INVALID", err.Error())
		return
	}
	d := &a.executions
	d.mu.Lock()
	defer d.mu.Unlock()
	a.expireExecutionsLocked(time.Now())
	if in.Mode == "platform" && (!d.enabled || time.Since(d.workerSeen) > 3*time.Second) {
		writeError(w, 503, "PLATFORM_UNAVAILABLE", "平台未启用或 Worker 离线；请明确选择本地方式后重新提交。")
		return
	}
	if len(d.entries) >= 32 {
		writeError(w, 429, "EXECUTION_QUEUE_FULL", "等待执行的任务已满。")
		return
	}
	id := newID("run")
	location := strings.TrimSpace(in.Location)
	if in.Mode == "platform" {
		location = d.location
	} else {
		if location == "" {
			location = "本机（位置未验证）"
		}
		if len(location) > 100 {
			writeError(w, 400, "EXECUTION_LOCATION_INVALID", "位置最多100字节。")
			return
		}
		location += " / " + id
	}
	snapshot := &ExecutionSnapshot{Mode: in.Mode, Location: location, Trust: "client_reported"}
	if in.Mode == "platform" {
		snapshot.Trust = "platform_worker"
	}
	run := Run{RunID: id, ExperimentID: experiment.ExperimentID, Status: "queued", SourceType: "openai_compatible", ResultKind: "measured", SoftwareVersion: "0.2.0", Config: experiment.Config, Execution: snapshot, CreatedAt: time.Now().UTC(), StageSummaries: []StageSummary{}}
	if err := a.store.createRun(owner, run); err != nil {
		if !a.creationError(w, err) {
			writeError(w, 500, "STORAGE_WRITE_FAILED", "无法保存执行记录。")
		}
		return
	}
	code := "claim_" + a.sign(newID("claim"))
	expires := time.Now().Add(5 * time.Minute)
	task := RunnerTask{TaskID: id, RunID: id, ExperimentID: experiment.ExperimentID, SourceType: run.SourceType, Config: run.Config, Execution: snapshot, ResultEndpoint: "/api/v1/execution-leases/" + id + "/result", ControlEndpoint: "/api/v1/execution-leases/" + id + "/control"}
	d.entries[id] = &executionEntry{owner: owner, codeHash: hashToken(code), key: in.APIKey, task: task, expires: expires}
	response := map[string]any{"run": run}
	if in.Mode == "local_agent" {
		response["claim_code"] = code
		response["expires_at"] = expires
	}
	writeJSON(w, 202, response)
}
func (a *App) claimExecutionLocked(w http.ResponseWriter, e *executionEntry) {
	if e.claimed {
		writeError(w, 409, "EXECUTION_ALREADY_CLAIMED", "任务已经领取。")
		return
	}
	e.claimed = true
	e.codeHash = ""
	e.lease = a.sign(newID("lease"))
	e.seen = time.Now()
	e.deadline = e.seen.Add(time.Duration(e.task.Config.Workload.StageSeconds*len(e.task.Config.Workload.ConcurrencySteps))*time.Second + 15*time.Second)
	e.task.TaskToken = e.lease
	run, err := a.store.run(e.owner, e.task.RunID)
	if err != nil {
		a.readError(w, err)
		return
	}
	now := time.Now().UTC()
	run.Status = "running"
	run.StartedAt = &now
	// Lock serializes cancellation/claim; conditional transition still protects DB state.
	if err := a.store.markExecutionRunning(e.owner, run); err != nil {
		a.failExecutionLocked(run.RunID, e, "EXECUTION_LEASE_LOST")
		writeError(w, 409, "EXECUTION_UNAVAILABLE", "任务已终止。")
		return
	}
	key := e.key
	e.key = ""
	writeJSON(w, 200, map[string]any{"task": e.task, "api_key": key})
}
func (a *App) claimExecution(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RunID string `json:"run_id"`
		Code  string `json:"code"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	d := &a.executions
	d.mu.Lock()
	defer d.mu.Unlock()
	a.expireExecutionsLocked(time.Now())
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	bucket := hashToken(ip)
	now := time.Now()
	for k, v := range d.rates {
		if now.After(v.until) {
			delete(d.rates, k)
		}
	}
	rate := d.rates[bucket]
	if rate.until.IsZero() {
		rate.until = now.Add(time.Minute)
	}
	rate.count++
	if len(d.rates) >= 1024 && d.rates[bucket].until.IsZero() || rate.count > 10 {
		writeError(w, 429, "CLAIM_RATE_LIMITED", "领取尝试过多，请稍后重试。")
		return
	}
	d.rates[bucket] = rate
	e := d.entries[in.RunID]
	if e == nil || e.task.Execution.Mode != "local_agent" || e.claimed || !hmac.Equal([]byte(e.codeHash), []byte(hashToken(in.Code))) || (requestIdentity(r).Owner != "" && requestIdentity(r).Owner != e.owner) {
		writeError(w, 403, "CLAIM_INVALID", "私人领取码无效、过期、已使用或不属于当前账户。")
		return
	}
	a.claimExecutionLocked(w, e)
}
func (a *App) workerPoll(w http.ResponseWriter, r *http.Request) {
	d := &a.executions
	if !d.enabled || !hmac.Equal([]byte(r.Header.Get("X-InferLab-Worker-Token")), []byte(d.workerToken)) {
		writeError(w, 403, "WORKER_UNAUTHORIZED", "worker identity required")
		return
	}
	var in struct {
		Instance string `json:"instance"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validRunnerID(in.Instance, "worker_") {
		writeError(w, 400, "WORKER_INVALID", "instance required")
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.workerSeen = time.Now()
	a.expireExecutionsLocked(time.Now())
	for _, e := range d.entries {
		if e.task.Execution.Mode == "platform" && !e.claimed {
			a.claimExecutionLocked(w, e)
			return
		}
	}
	w.WriteHeader(204)
}
func (a *App) executionLease(w http.ResponseWriter, r *http.Request) {
	var result RunnerResult
	if r.Method == "POST" && !decodeJSON(w, r, &result) {
		return
	}
	d := &a.executions
	d.mu.Lock()
	defer d.mu.Unlock()
	a.expireExecutionsLocked(time.Now())
	e := d.entries[r.PathValue("id")]
	if e == nil || !e.claimed || !hmac.Equal([]byte(e.lease), []byte(r.Header.Get("X-InferLab-Runner-Token"))) {
		writeError(w, 409, "EXECUTION_LEASE_LOST", "任务已取消、结束或租约失效。")
		return
	}
	if r.Method == "GET" {
		if e.task.Execution.Mode == "platform" {
			d.workerSeen = time.Now()
		}
		e.seen = time.Now()
		writeJSON(w, 200, map[string]string{"status": "running"})
		return
	}
	if result.RunID != e.task.RunID || result.validateFor(e.task.Config) != nil {
		writeError(w, 400, "INVALID_RUNNER_RESULT", "执行结果与任务不匹配。")
		return
	}
	run, err := a.store.run(e.owner, e.task.RunID)
	if err != nil {
		a.readError(w, err)
		return
	}
	run.Status = result.Status
	run.Metrics = result.Metrics
	run.CurrentStage = result.CurrentStage
	run.StageSummaries = result.StageSummaries
	run.StartedAt = result.StartedAt
	run.FinishedAt = result.FinishedAt
	run.ErrorCode = result.ErrorCode
	run.Diagnostics = result.Diagnostics
	if err := a.store.finishLocalRun(e.owner, run, "execution_"+result.Status); err != nil {
		writeError(w, 409, "RUN_ALREADY_FINISHED", "运行已经结束。")
		return
	}
	e.key = ""
	delete(d.entries, run.RunID)
	writeJSON(w, 202, run)
}

func (s *Store) markExecutionRunning(owner string, run Run) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	result, err := s.db.Exec("UPDATE runs SET status='running',data=? WHERE id=? AND owner=? AND status='queued'", data, run.RunID, owner)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errRunAlreadyFinished
	}
	return err
}
