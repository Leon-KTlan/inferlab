package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const intelligenceTaskTTL = 15 * time.Minute

type intelligenceCreateRequest struct {
	Name        string `json:"name"`
	Target      Target `json:"target"`
	MockProfile string `json:"mock_profile,omitempty"`
	Repetitions *int   `json:"repetitions"`
	Authorized  bool   `json:"authorized,omitempty"`
	APIKey      string `json:"api_key,omitempty"`
}

type IntelligenceRunnerResult struct {
	RunID      string                `json:"run_id"`
	Status     string                `json:"status"`
	Outcomes   []IntelligenceOutcome `json:"outcomes"`
	StartedAt  *time.Time            `json:"started_at"`
	FinishedAt *time.Time            `json:"finished_at"`
	ErrorCode  string                `json:"error_code,omitempty"`
}

func (a *App) registerIntelligenceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/intelligence/suite", a.getIntelligenceSuite)
	mux.HandleFunc("POST /api/v1/intelligence/runs", a.createIntelligenceRun)
	mux.HandleFunc("GET /api/v1/intelligence/runs", a.listIntelligenceRuns)
	mux.HandleFunc("GET /api/v1/intelligence/runs/{id}", a.getIntelligenceRun)
	mux.HandleFunc("POST /api/v1/intelligence/runs/{id}/cancel", a.cancelIntelligenceRun)
	mux.HandleFunc("POST /api/v1/intelligence/runs/{id}/rerun", a.rerunIntelligence)
	mux.HandleFunc("POST /api/v1/intelligence/runs/{id}/result", a.submitIntelligenceResult)
	mux.HandleFunc("GET /api/v1/intelligence/radar", a.getIntelligenceRadar)
}

func (a *App) getIntelligenceSuite(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"suite_version": intelligenceSuiteVersion, "probes": intelligenceProbes(), "dimensions": intelligenceDimensions()})
}

func intelligenceConfig(input intelligenceCreateRequest) (Config, error) {
	repetitions := 2
	if input.Repetitions != nil {
		repetitions = *input.Repetitions
	}
	if repetitions < 1 || repetitions > 3 {
		return Config{}, errors.New("repetitions must be between 1 and 3")
	}
	if input.Target.MockScenario != "" {
		return Config{}, errors.New("mock_scenario is not supported for intelligence tests")
	}
	if input.Target.Type == "mock" && input.MockProfile == "" {
		input.MockProfile = "reference"
	}
	if input.Target.Type == "mock" && input.MockProfile != "reference" && input.MockProfile != "degraded" {
		return Config{}, errors.New("mock_profile must be reference or degraded")
	}
	if input.Target.Type != "mock" && input.MockProfile != "" {
		return Config{}, errors.New("mock_profile is only available for mock targets")
	}
	config := Config{Name: input.Name, Target: input.Target, Workload: Workload{ConcurrencySteps: []int{1}, StageSeconds: 30, MaxRequests: len(intelligenceProbes()) * repetitions, MaxRPS: 1, TimeoutMS: 10000}, SLO: SLO{P95E2EMS: 10000, ErrorRateMax: 1}}
	if err := config.validate(); err != nil {
		return Config{}, err
	}
	if config.Target.Type == "openai_compatible" {
		endpoint, _ := url.Parse(strings.TrimSpace(config.Target.Endpoint))
		endpoint.Host = strings.TrimSuffix(strings.ToLower(endpoint.Host), ":443")
		endpoint.Path = strings.TrimRight(endpoint.Path, "/")
		endpoint.RawPath = strings.TrimRight(endpoint.RawPath, "/")
		config.Target.Endpoint = endpoint.String()
	}
	config.Intelligence = &IntelligenceSettings{SuiteVersion: intelligenceSuiteVersion, Repetitions: repetitions, MockProfile: input.MockProfile}
	return config, nil
}

func validIntelligenceCredential(w http.ResponseWriter, config Config, authorized bool, key string) bool {
	if config.Target.Type != "mock" && !authorized {
		writeError(w, 400, "TARGET_AUTHORIZATION_REQUIRED", "explicit target authorization is required")
		return false
	}
	if len(key) > 4096 || strings.ContainsAny(key, "\r\n") || config.Target.Type == "mock" && key != "" {
		writeError(w, 400, "INVALID_API_KEY", "API Key is invalid for this target")
		return false
	}
	return true
}

func (a *App) createIntelligenceRun(w http.ResponseWriter, r *http.Request) {
	if !a.requireLogin(w, r) {
		return
	}
	var input intelligenceCreateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	config, err := intelligenceConfig(input)
	if err != nil {
		writeError(w, 400, "INVALID_INTELLIGENCE_CONFIG", err.Error())
		return
	}
	if !validIntelligenceCredential(w, config, input.Authorized, input.APIKey) {
		return
	}
	owner := a.owner(w, r)
	experiment := Experiment{ExperimentID: newID("exp"), Config: config, CreatedAt: time.Now().UTC()}
	a.startIntelligence(w, owner, experiment, input.APIKey, true)
}

func (a *App) startIntelligence(w http.ResponseWriter, owner string, experiment Experiment, key string, createExperiment bool) {
	runID := newID("run")
	if key != "" && !a.credentials.put(runID, key) {
		writeError(w, 429, "CREDENTIAL_QUOTA_EXCEEDED", "temporary credential capacity reached")
		return
	}
	run, err := a.intelligence.start(owner, experiment, runID, createExperiment)
	if err != nil {
		a.credentials.remove(runID)
		if !a.creationError(w, err) {
			writeError(w, 500, "STORAGE_WRITE_FAILED", "could not start intelligence run")
		}
		return
	}
	response := map[string]any{"run": run}
	if run.SourceType == "openai_compatible" {
		response["task"] = RunnerTask{TaskID: runID, RunID: runID, ExperimentID: experiment.ExperimentID, SourceType: run.SourceType, Config: run.Config, TaskToken: a.runnerToken(runID), ResultEndpoint: "/api/v1/intelligence/runs/" + runID + "/result", CredentialAvailable: key != ""}
	}
	writeJSON(w, 202, response)
}

func (a *App) intelligenceRun(w http.ResponseWriter, r *http.Request) (Run, bool) {
	run, err := a.store.run(a.owner(w, r), r.PathValue("id"))
	if err != nil {
		a.readError(w, err)
		return Run{}, false
	}
	if run.Config.Intelligence == nil {
		writeError(w, 404, "NOT_FOUND", "intelligence run not found")
		return Run{}, false
	}
	return run, true
}

func (s *Store) intelligenceRuns(owner string) ([]Run, error) {
	// Filter before LIMIT so performance runs cannot hide intelligence history.
	rows, err := s.db.Query("SELECT data FROM runs WHERE owner=? AND json_type(data,'$.config.intelligence')='object' ORDER BY rowid DESC LIMIT 100", owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var run Run
		if err := json.Unmarshal(data, &run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (a *App) listIntelligenceRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := a.store.intelligenceRuns(a.owner(w, r))
	if err != nil {
		a.readError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}

func (a *App) getIntelligenceRun(w http.ResponseWriter, r *http.Request) {
	if run, ok := a.intelligenceRun(w, r); ok {
		writeJSON(w, 200, run)
	}
}

func (a *App) rerunIntelligence(w http.ResponseWriter, r *http.Request) {
	if !a.requireLogin(w, r) {
		return
	}
	source, ok := a.intelligenceRun(w, r)
	if !ok {
		return
	}
	if !source.terminal() {
		writeError(w, 409, "RUN_NOT_FINISHED", "only terminal intelligence runs can be repeated")
		return
	}
	var input struct {
		Authorized bool   `json:"authorized"`
		APIKey     string `json:"api_key"`
	}
	if r.Body != nil && r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	if !validIntelligenceCredential(w, source.Config, input.Authorized, input.APIKey) {
		return
	}
	a.startIntelligence(w, a.owner(w, r), Experiment{ExperimentID: source.ExperimentID, Config: source.Config}, input.APIKey, false)
}

func (a *App) cancelIntelligenceRun(w http.ResponseWriter, r *http.Request) {
	run, ok := a.intelligenceRun(w, r)
	if !ok {
		return
	}
	if !run.terminal() {
		if run.SourceType == "mock" {
			a.intelligence.cancel(run.RunID)
		} else {
			now := time.Now().UTC()
			run.Status = "canceled"
			run.ErrorCode = "RUN_CANCELED"
			run.FinishedAt = &now
			updateIntelligenceCounts(&run)
			if err := a.store.finishLocalRun(a.owner(w, r), run, "intelligence_canceled"); err != nil {
				if !errors.Is(err, errRunAlreadyFinished) {
					writeError(w, 500, "STORAGE_WRITE_FAILED", "could not cancel intelligence run")
					return
				}
				current, readErr := a.store.run(a.owner(w, r), run.RunID)
				if readErr != nil {
					a.readError(w, readErr)
					return
				}
				run = current
			}
			a.intelligence.cancel(run.RunID)
		}
	}
	a.credentials.remove(run.RunID)
	writeJSON(w, 202, map[string]any{"run_id": run.RunID, "status": run.Status, "cancel_requested": !run.terminal()})
}

func (result IntelligenceRunnerResult) validateFor(config Config) error {
	settings := config.Intelligence
	if settings == nil || settings.SuiteVersion != intelligenceSuiteVersion || settings.Repetitions < 1 || settings.Repetitions > 3 {
		return errors.New("unsupported intelligence settings")
	}
	if result.Status != "succeeded" && result.Status != "failed" && result.Status != "canceled" {
		return errors.New("invalid intelligence result status")
	}
	if result.StartedAt == nil || result.FinishedAt == nil || result.StartedAt.IsZero() || result.FinishedAt.Before(*result.StartedAt) || result.FinishedAt.Sub(*result.StartedAt) > 121*time.Second {
		return errors.New("invalid intelligence observation window")
	}
	if len(result.Outcomes) > len(intelligenceProbes())*settings.Repetitions {
		return errors.New("too many intelligence outcomes")
	}
	ids := map[string]bool{}
	for _, probe := range intelligenceProbes() {
		ids[probe.ID] = true
	}
	seen := map[string]bool{}
	totalLatency := 0.0
	for _, outcome := range result.Outcomes {
		key := fmt.Sprintf("%s/%d", outcome.ProbeID, outcome.Repetition)
		if !ids[outcome.ProbeID] || outcome.Repetition < 1 || outcome.Repetition > settings.Repetitions || seen[key] {
			return errors.New("invalid or duplicate intelligence outcome")
		}
		seen[key] = true
		if math.IsNaN(outcome.LatencyMS) || math.IsInf(outcome.LatencyMS, 0) || outcome.LatencyMS < 0 || outcome.LatencyMS > 11000 || outcome.LatencyMS > result.FinishedAt.Sub(*result.StartedAt).Seconds()*1000+50 {
			return errors.New("invalid intelligence latency")
		}
		totalLatency += outcome.LatencyMS
		if outcome.ErrorCode != "" && (!isIntelligenceErrorCode(outcome.ErrorCode) || outcome.Passed) {
			return errors.New("invalid intelligence outcome error")
		}
	}
	if totalLatency > result.FinishedAt.Sub(*result.StartedAt).Seconds()*1000+50 {
		return errors.New("intelligence latencies exceed sequential observation window")
	}
	if result.Status == "succeeded" && (len(result.Outcomes) != len(intelligenceProbes())*settings.Repetitions || result.ErrorCode != "") {
		return errors.New("successful intelligence result is incomplete")
	}
	if result.Status == "failed" && !isIntelligenceErrorCode(result.ErrorCode) {
		return errors.New("invalid intelligence failure code")
	}
	if result.Status == "canceled" && result.ErrorCode != "RUN_CANCELED" {
		return errors.New("invalid intelligence cancellation code")
	}
	return nil
}

func isIntelligenceErrorCode(code string) bool {
	return isRunnerErrorCode(code) || code == "RUN_TIMEOUT" || code == "TASK_EXPIRED"
}

func (a *App) submitIntelligenceResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.validRunnerToken(id, r.Header.Get("X-InferLab-Runner-Token")) {
		writeError(w, 403, "RUNNER_TOKEN_INVALID", "valid task token required")
		return
	}
	owner, err := a.store.runOwner(id)
	if err != nil {
		a.readError(w, err)
		return
	}
	run, err := a.store.run(owner, id)
	if err != nil {
		a.readError(w, err)
		return
	}
	if run.Config.Intelligence == nil || run.SourceType != "openai_compatible" {
		writeError(w, 400, "LOCAL_RUNNER_TARGET_REQUIRED", "run is not an intelligence local runner task")
		return
	}
	if run.terminal() {
		writeError(w, 409, "RUN_ALREADY_FINISHED", "run is already terminal")
		return
	}
	if time.Since(run.CreatedAt) > intelligenceTaskTTL {
		writeError(w, 410, "TASK_EXPIRED", "intelligence task has expired")
		return
	}
	// Only bounded, structured outcomes are accepted; prompts and response text have no fields.
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	var result IntelligenceRunnerResult
	if !decodeJSON(w, r, &result) {
		return
	}
	if result.RunID != id {
		writeError(w, 400, "RUN_ID_MISMATCH", "result does not match task")
		return
	}
	if err := result.validateFor(run.Config); err != nil {
		writeError(w, 400, "INVALID_RUNNER_RESULT", err.Error())
		return
	}
	now := time.Now().UTC()
	if result.StartedAt.Before(run.CreatedAt.Add(-5*time.Second)) || result.FinishedAt.After(now.Add(5*time.Second)) {
		writeError(w, 400, "INVALID_RUNNER_RESULT", "observation window is outside task lifetime")
		return
	}
	report := summarizeIntelligence(*run.Config.Intelligence, result.Outcomes)
	run.Status = result.Status
	run.StartedAt = result.StartedAt
	run.FinishedAt = result.FinishedAt
	run.ErrorCode = result.ErrorCode
	run.IntelligenceReport = &report
	updateIntelligenceCounts(&run)
	if err := a.store.finishLocalRun(owner, run, "intelligence_"+result.Status); err != nil {
		if errors.Is(err, errRunAlreadyFinished) {
			writeError(w, 409, "RUN_ALREADY_FINISHED", "run reached a terminal state before result acceptance")
		} else {
			writeError(w, 500, "STORAGE_WRITE_FAILED", "could not save intelligence result")
		}
		return
	}
	a.credentials.remove(id)
	a.intelligence.cancel(id)
	writeJSON(w, 202, run)
}

// Capability outcomes expose transport counts without inventing performance percentiles.
func updateIntelligenceCounts(run *Run) {
	report := run.IntelligenceReport
	if report == nil {
		return
	}
	run.RequestCount = report.Completed
	run.CompletedCount = report.Completed
	run.ErrorCount = report.ErrorCount
	run.SuccessCount = report.Completed - report.ErrorCount
	if run.StartedAt != nil {
		end := time.Now().UTC()
		if run.FinishedAt != nil {
			end = *run.FinishedAt
		}
		run.DurationSeconds = end.Sub(*run.StartedAt).Seconds()
	}
}
