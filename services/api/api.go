package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type App struct {
	store       *Store
	runner      *Runner
	mock        *MockAdapter
	secret      []byte
	secure      bool
	handler     http.Handler
	credentials runnerCredentials
}

func newID(prefix string) string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("system entropy unavailable")
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(b)
}

func newApp(databasePath string, secret []byte, secureEnv bool, webDir string) (*App, error) {
	if secureEnv && len(secret) < 32 {
		return nil, errors.New("SESSION_SECRET must contain at least 32 bytes in staging or production")
	}
	store, err := openStore(databasePath)
	if err != nil {
		return nil, err
	}
	cleanup := func(err error) (*App, error) { store.db.Close(); return nil, err }
	if value := os.Getenv("DAILY_REQUEST_LIMIT"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 1000000000 {
			return cleanup(errors.New("DAILY_REQUEST_LIMIT must be an integer between 1 and 1000000000"))
		}
		store.dailyLimit = limit
	}
	if len(secret) == 0 {
		secret, err = store.localSecret([]byte(newID("session")))
		if err != nil {
			return cleanup(err)
		}
	}
	if err := store.recoverRuns(); err != nil {
		return cleanup(err)
	}
	mock, err := newMockAdapter()
	if err != nil {
		return cleanup(err)
	}
	a := &App{store: store, runner: newRunner(store, mock), mock: mock, secret: secret, secure: secureEnv}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /api/v1/healthz", a.health)
	mux.HandleFunc("GET /api/v1/auth/me", a.authMe)
	mux.HandleFunc("POST /api/v1/auth/register", a.register)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("POST /api/v1/auth/logout", a.logout)
	mux.HandleFunc("POST /api/v1/auth/setup", a.setupAdmin)
	mux.HandleFunc("GET /api/v1/admin/registration", a.getRegistrationSettings)
	mux.HandleFunc("PUT /api/v1/admin/registration", a.putRegistrationSettings)
	mux.HandleFunc("POST /api/v1/experiments", a.createExperiment)
	mux.HandleFunc("GET /api/v1/experiments", a.listExperiments)
	mux.HandleFunc("GET /api/v1/experiments/{id}", a.getExperiment)
	mux.HandleFunc("DELETE /api/v1/experiments/{id}", a.deleteExperiment)
	mux.HandleFunc("POST /api/v1/experiments/{id}/runs", a.startRun)
	mux.HandleFunc("POST /api/v1/experiments/{id}/runner-tasks", a.createRunnerTask)
	mux.HandleFunc("GET /api/v1/runs/{id}", a.getRun)
	mux.HandleFunc("DELETE /api/v1/runs/{id}", a.deleteRun)
	mux.HandleFunc("POST /api/v1/runs/{id}/share", a.createShare)
	mux.HandleFunc("DELETE /api/v1/runs/{id}/share", a.revokeShare)
	mux.HandleFunc("GET /api/v1/shares/{token}", a.getShare)
	mux.HandleFunc("GET /api/v1/runs", a.listRuns)
	mux.HandleFunc("POST /api/v1/runs/{id}/cancel", a.cancelRun)
	mux.HandleFunc("POST /api/v1/runner-tasks/{id}/result", a.submitRunnerResult)
	mux.HandleFunc("POST /api/v1/runner-tasks/{id}/credential", a.claimRunnerCredential)
	mux.HandleFunc("GET /api/v1/runs/{id}/events", a.getEvents)
	mux.HandleFunc("GET /api/v1/runs/{id}/report", a.getReport)
	mux.HandleFunc("GET /api/v1/reports/{id}/compare", a.compareReports)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "API route not found")
	})
	if webDir != "" {
		mux.Handle("/", spaHandler(webDir))
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
		})
	}
	apiHandler := a.identityMiddleware(mux)
	a.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; base-uri 'self'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			// Browser requests must originate from the same site; no cross-origin API access is enabled.
			if !sameOrigin(r, a.secure) {
				writeError(w, http.StatusForbidden, "CROSS_SITE_REQUEST", "cross-site API requests are not allowed")
				return
			}
		}
		apiHandler.ServeHTTP(w, r)
	})
	return a, nil
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.handler.ServeHTTP(w, r) }
func (a *App) Close()                                           { a.runner.Close(); a.credentials.close(); a.mock.Close(); a.store.db.Close() }

func spaHandler(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			writeError(w, 405, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		clean := filepath.Clean("/" + r.URL.Path)
		if strings.HasPrefix(clean, "/.") {
			http.NotFound(w, r)
			return
		}
		info, err := os.Stat(filepath.Join(dir, clean))
		if err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		if filepath.Ext(clean) != "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (a *App) sign(value string) string {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *App) runnerToken(runID string) string { return a.sign("runner:" + runID) }

func (a *App) validRunnerToken(runID, token string) bool {
	return hmac.Equal([]byte(strings.TrimSpace(token)), []byte(a.runnerToken(runID)))
}
func (a *App) owner(w http.ResponseWriter, r *http.Request) string {
	return requestIdentity(r).Owner
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	if err := a.store.healthy(r.Context()); err != nil {
		writeError(w, 503, "DATABASE_UNAVAILABLE", "database unavailable")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok", "version": "0.1.0"})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeError(w, 415, "CONTENT_TYPE_REQUIRED", "use application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, 400, "INVALID_JSON", "invalid or unsupported JSON body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, 400, "INVALID_JSON", "provide exactly one JSON object")
		return false
	}
	return true
}

func (a *App) createExperiment(w http.ResponseWriter, r *http.Request) {
	owner := a.owner(w, r)
	var config Config
	if !decodeJSON(w, r, &config) {
		return
	}
	if err := config.validate(); err != nil {
		writeError(w, 400, "INVALID_EXPERIMENT_CONFIG", err.Error())
		return
	}
	e := Experiment{ExperimentID: newID("exp"), Config: config, CreatedAt: time.Now().UTC()}
	if err := a.store.createExperiment(owner, e); err != nil {
		if a.creationError(w, err) {
			return
		}
		writeError(w, 500, "STORAGE_WRITE_FAILED", "could not save experiment")
		return
	}
	writeJSON(w, 201, e)
}

func (a *App) listExperiments(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.experiments(a.owner(w, r))
	if err != nil {
		writeError(w, 500, "STORAGE_READ_FAILED", "could not read experiments")
		return
	}
	writeJSON(w, 200, map[string]any{"experiments": list})
}

func (a *App) getExperiment(w http.ResponseWriter, r *http.Request) {
	e, err := a.store.experiment(a.owner(w, r), r.PathValue("id"))
	if err != nil {
		a.readError(w, err)
		return
	}
	writeJSON(w, 200, e)
}

func (a *App) readError(w http.ResponseWriter, err error) {
	if isNotFound(err) {
		writeError(w, 404, "NOT_FOUND", "record not found")
		return
	}
	writeError(w, 500, "STORAGE_READ_FAILED", "could not read record")
}

func (a *App) startRun(w http.ResponseWriter, r *http.Request) {
	if !a.requireLogin(w, r) {
		return
	}
	owner := a.owner(w, r)
	e, err := a.store.experiment(owner, r.PathValue("id"))
	if err != nil {
		a.readError(w, err)
		return
	}
	run, err := a.runner.start(owner, e)
	if a.creationError(w, err) {
		return
	}
	if errors.Is(err, errLocalRunnerRequired) {
		writeError(w, http.StatusConflict, "LOCAL_RUNNER_REQUIRED", "OpenAI-compatible targets must run through the local runner task protocol")
		return
	}
	if err != nil {
		writeError(w, 500, "STORAGE_WRITE_FAILED", "could not start run")
		return
	}
	writeJSON(w, 202, run)
}

func (a *App) createRunnerTask(w http.ResponseWriter, r *http.Request) {
	if !a.requireLogin(w, r) {
		return
	}
	owner := a.owner(w, r)
	experiment, err := a.store.experiment(owner, r.PathValue("id"))
	if err != nil {
		a.readError(w, err)
		return
	}
	if experiment.Target.Type != "openai_compatible" {
		writeError(w, http.StatusBadRequest, "LOCAL_RUNNER_TARGET_REQUIRED", "runner tasks are only available for OpenAI-compatible targets")
		return
	}
	var input struct {
		APIKey string `json:"api_key"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSON(w, r, &input) {
			return
		}
	}
	if len(input.APIKey) > 4096 || strings.ContainsAny(input.APIKey, "\r\n") {
		writeError(w, 400, "INVALID_API_KEY", "API Key is invalid")
		return
	}
	runs, err := a.store.runs(owner)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORAGE_READ_FAILED", "could not read runs")
		return
	}
	for _, item := range runs {
		if !item.terminal() {
			writeError(w, http.StatusTooManyRequests, "RUN_QUOTA_EXCEEDED", "one active run per visitor is allowed")
			return
		}
	}
	runID := newID("run")
	if input.APIKey != "" {
		if !a.credentials.put(runID, input.APIKey) {
			writeError(w, 429, "CREDENTIAL_QUOTA_EXCEEDED", "temporary credential capacity reached")
			return
		}
	}
	run := Run{RunID: runID, ExperimentID: experiment.ExperimentID, Status: "queued", SourceType: "openai_compatible", ResultKind: "measured", SoftwareVersion: "0.1.0", Config: experiment.Config, CreatedAt: time.Now().UTC(), StageSummaries: []StageSummary{}}
	if err := a.store.createRun(owner, run); err != nil {
		a.credentials.remove(runID)
		if a.creationError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "STORAGE_WRITE_FAILED", "could not create runner task")
		return
	}
	task := RunnerTask{TaskID: runID, RunID: runID, ExperimentID: experiment.ExperimentID, SourceType: "openai_compatible", Config: experiment.Config, TaskToken: a.runnerToken(runID), ResultEndpoint: "/api/v1/runner-tasks/" + runID + "/result"}
	task.CredentialAvailable = input.APIKey != ""
	writeJSON(w, http.StatusAccepted, task)
}

func (a *App) creationError(w http.ResponseWriter, err error) bool {
	var quota *quotaError
	if errors.As(err, &quota) {
		writeJSON(w, 429, map[string]any{"error": map[string]any{"code": "DAILY_REQUEST_QUOTA_EXCEEDED", "message": "daily reserved request quota exhausted", "quota": quota.Quota}})
		return true
	}
	if errors.Is(err, errSessionChanged) {
		writeError(w, 409, "SESSION_CHANGED", "session changed; refresh before retrying")
		return true
	}
	if errors.Is(err, errBusy) {
		writeError(w, 429, "RUN_QUOTA_EXCEEDED", "one active run per owner and four globally are allowed")
		return true
	}
	return false
}

func (a *App) submitRunnerResult(w http.ResponseWriter, r *http.Request) {
	owner := a.owner(w, r)
	run, err := a.store.run(owner, r.PathValue("id"))
	if isNotFound(err) && a.validRunnerToken(r.PathValue("id"), r.Header.Get("X-InferLab-Runner-Token")) {
		owner, err = a.store.runOwner(r.PathValue("id"))
		if err == nil {
			run, err = a.store.run(owner, r.PathValue("id"))
		}
	}
	if err != nil {
		a.readError(w, err)
		return
	}
	if run.SourceType != "openai_compatible" {
		writeError(w, http.StatusBadRequest, "LOCAL_RUNNER_TARGET_REQUIRED", "run is not owned by a local runner")
		return
	}
	if run.terminal() {
		writeError(w, http.StatusConflict, "RUN_ALREADY_FINISHED", "runner result was already accepted")
		return
	}
	var result RunnerResult
	if !decodeJSON(w, r, &result) {
		return
	}
	if result.RunID != run.RunID {
		writeError(w, http.StatusBadRequest, "RUN_ID_MISMATCH", "runner result does not match the task")
		return
	}
	if err := result.validateFor(run.Config); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_RUNNER_RESULT", err.Error())
		return
	}
	now := time.Now().UTC()
	if result.StartedAt == nil {
		result.StartedAt = &now
	}
	if result.FinishedAt == nil {
		result.FinishedAt = &now
	}
	run.Status = result.Status
	run.Metrics = result.Metrics
	run.CurrentStage = result.CurrentStage
	run.StageSummaries = result.StageSummaries
	run.StartedAt = result.StartedAt
	run.FinishedAt = result.FinishedAt
	run.ErrorCode = result.ErrorCode
	run.Diagnostics = result.Diagnostics
	if err := a.store.finishLocalRun(owner, run, "local_runner_"+result.Status); err != nil {
		if errors.Is(err, errRunAlreadyFinished) {
			writeError(w, http.StatusConflict, "RUN_ALREADY_FINISHED", "run reached a terminal state before this result was accepted")
			return
		}
		writeError(w, http.StatusInternalServerError, "STORAGE_WRITE_FAILED", "could not save runner result")
		return
	}
	a.credentials.remove(run.RunID)
	writeJSON(w, http.StatusAccepted, run)
}

func (a *App) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.run(a.owner(w, r), r.PathValue("id"))
	if err != nil {
		a.readError(w, err)
		return
	}
	writeJSON(w, 200, run)
}

func (a *App) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := a.store.runs(a.owner(w, r))
	if err != nil {
		writeError(w, 500, "STORAGE_READ_FAILED", "could not read runs")
		return
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}

func (a *App) cancelRun(w http.ResponseWriter, r *http.Request) {
	owner := a.owner(w, r)
	run, err := a.store.run(owner, r.PathValue("id"))
	if err != nil {
		a.readError(w, err)
		return
	}
	if !run.terminal() && run.SourceType == "openai_compatible" {
		now := time.Now().UTC()
		run.Status = "canceled"
		run.ErrorCode = "RUN_CANCELED"
		run.FinishedAt = &now
		if err := a.store.finishLocalRun(owner, run, "run_canceled"); err != nil {
			if !errors.Is(err, errRunAlreadyFinished) {
				writeError(w, http.StatusInternalServerError, "STORAGE_WRITE_FAILED", "could not cancel run")
				return
			}
			run, err = a.store.run(owner, run.RunID)
			if err != nil {
				a.readError(w, err)
				return
			}
		}
	} else if !run.terminal() {
		a.runner.cancel(owner, run.RunID)
	}
	if run.terminal() {
		a.credentials.remove(run.RunID)
	}
	writeJSON(w, 202, map[string]any{"run_id": run.RunID, "status": run.Status, "cancel_requested": !run.terminal()})
}

func (a *App) getEvents(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.run(a.owner(w, r), r.PathValue("id"))
	if err != nil {
		a.readError(w, err)
		return
	}
	var after int64
	if value := r.URL.Query().Get("after"); value != "" {
		after, err = strconv.ParseInt(value, 10, 64)
		if err != nil || after < 0 {
			writeError(w, 400, "INVALID_CURSOR", "after must be a non-negative event ID")
			return
		}
	}
	events, err := a.store.events(run.RunID, after)
	if err != nil {
		a.readError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"events": events})
}

type Report struct {
	Run
	SLOStatus       string   `json:"slo_status"`
	Recommendations []string `json:"recommendations"`
	Methodology     string   `json:"methodology"`
	Note            string   `json:"note"`
}

func (a *App) getReport(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.run(a.owner(w, r), r.PathValue("id"))
	if err != nil {
		a.readError(w, err)
		return
	}
	if !run.terminal() {
		writeError(w, 409, "RUN_NOT_FINISHED", "report is available when the run finishes")
		return
	}
	status := "pass"
	recommendations := []string{}
	for _, stage := range run.StageSummaries {
		if stage.SLOStatus == "fail" {
			status = "fail"
			recommendations = append(recommendations, fmt.Sprintf("阶段 %d（并发 %d）P95 %.1fms，错误率 %.1f%%；请降低负载并复测。", stage.Stage, stage.Concurrency, stage.P95E2EMS, stage.ErrorRate*100))
		} else if stage.SLOStatus == "insufficient_data" {
			if status != "fail" {
				status = "insufficient_data"
			}
			recommendations = append(recommendations, fmt.Sprintf("阶段 %d 仅有 %d 个完成样本；请增加阶段时长或请求预算后复测。", stage.Stage, stage.CompletedCount))
		}
	}
	if len(run.StageSummaries) < len(run.Config.Workload.ConcurrencySteps) || run.Status != "succeeded" {
		if status != "fail" {
			status = "insufficient_data"
		}
		recommendations = append(recommendations, "运行未覆盖全部配置阶段，请使用足够请求预算完成复测。")
	}
	if len(recommendations) == 0 {
		recommendations = append(recommendations, "已采样阶段满足设定阈值；请在授权真实目标上复测后评估上线。")
	}
	note := "Mock 实测：请求发送到进程内本地 HTTP 模拟服务，不代表真实模型服务性能。"
	if run.SourceType == "openai_compatible" {
		note = "本地 Runner 实测：指标由用户本地进程对授权 OpenAI-compatible 目标采样后回传；服务端不保存目标凭证、Prompt 或响应正文，也未独立复测这些数据。"
	}
	writeJSON(w, 200, Report{Run: run, SLOStatus: status, Recommendations: recommendations, Methodology: "P50/P95 使用所有已完成请求耗时的 nearest-rank 分位数（包含错误和取消）；总体分位数由总体原始采样计算。错误率=错误数/完成数；吞吐=完成数/实际观察秒数；少于20个样本标记数据不足。", Note: note})
}
