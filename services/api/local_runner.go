package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// RunnerTask is the credential-free handoff between the API and a local runner.
// The API key is deliberately absent and must stay in the local runner process.
type RunnerTask struct {
	Execution           *ExecutionSnapshot `json:"execution,omitempty"`
	ControlEndpoint     string             `json:"control_endpoint,omitempty"`
	TaskID              string             `json:"task_id"`
	RunID               string             `json:"run_id"`
	ExperimentID        string             `json:"experiment_id"`
	SourceType          string             `json:"source_type"`
	Config              Config             `json:"config"`
	TaskToken           string             `json:"task_token"`
	ResultEndpoint      string             `json:"result_endpoint"`
	CredentialAvailable bool               `json:"credential_available,omitempty"`
}

type RunnerResult struct {
	RunID          string              `json:"run_id"`
	Status         string              `json:"status"`
	Metrics        Metrics             `json:"metrics"`
	CurrentStage   int                 `json:"current_stage"`
	StageSummaries []StageSummary      `json:"stage_summaries"`
	StartedAt      *time.Time          `json:"started_at,omitempty"`
	FinishedAt     *time.Time          `json:"finished_at,omitempty"`
	ErrorCode      string              `json:"error_code,omitempty"`
	Diagnostics    []RequestDiagnostic `json:"diagnostics,omitempty"`
}

func executeLocalTask(ctx context.Context, task RunnerTask, apiKey string) (RunnerResult, error) {
	adapter, err := newOpenAICompatibleAdapter(task.Config.Target, apiKey)
	if err != nil {
		return RunnerResult{}, err
	}
	defer adapter.Close()
	return executeLocalTaskWithAdapter(ctx, task, adapter)
}

func executeLocalTaskWithAdapter(ctx context.Context, task RunnerTask, adapter Adapter) (RunnerResult, error) {
	if task.SourceType != "openai_compatible" || task.Config.Target.Type != "openai_compatible" {
		return RunnerResult{}, errors.New("LOCAL_RUNNER_TARGET_REQUIRED")
	}
	if err := validateLocalConfig(ctx, task.Config, EndpointPolicy{AllowLoopback: true}); err != nil {
		return RunnerResult{}, err
	}
	store, err := openStore(":memory:")
	if err != nil {
		return RunnerResult{}, err
	}
	defer store.db.Close()
	experimentID := task.ExperimentID
	if experimentID == "" {
		experimentID = task.Config.Name
	}
	experiment := Experiment{ExperimentID: experimentID, Config: task.Config}
	if err := store.createExperiment("local-runner", experiment); err != nil {
		return RunnerResult{}, err
	}
	runner := newRunner(store, adapter)
	run, err := runner.startWithContext(ctx, "local-runner", experiment, "openai_compatible", "measured", task.RunID)
	if err != nil {
		return RunnerResult{}, err
	}
	defer runner.Close()
	for {
		current, readErr := store.run("local-runner", run.RunID)
		if readErr != nil {
			return RunnerResult{}, readErr
		}
		if current.terminal() {
			return RunnerResult{RunID: current.RunID, Status: current.Status, Metrics: current.Metrics, CurrentStage: current.CurrentStage, StageSummaries: current.StageSummaries, StartedAt: current.StartedAt, FinishedAt: current.FinishedAt, ErrorCode: current.ErrorCode}, nil
		}
		select {
		case <-ctx.Done():
			runner.cancel("local-runner", run.RunID)
			runner.Close()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (r RunnerResult) validateFor(config Config) error {
	if r.Status != "succeeded" && r.Status != "failed" && r.Status != "canceled" {
		return fmt.Errorf("invalid local runner status")
	}
	if !validRunnerMetrics(r.Metrics, config.Workload.MaxRequests) {
		return fmt.Errorf("invalid local runner metrics")
	}
	if err := validateDiagnostics(r.Diagnostics, r.Metrics, r.StartedAt, r.FinishedAt); err != nil {
		return err
	}
	if r.StartedAt == nil || r.FinishedAt == nil || r.StartedAt.IsZero() || r.FinishedAt.Before(*r.StartedAt) || r.Metrics.DurationSeconds > r.FinishedAt.Sub(*r.StartedAt).Seconds()+0.05 {
		return fmt.Errorf("invalid local runner observation window")
	}
	if len(r.StageSummaries) > len(config.Workload.ConcurrencySteps) || r.CurrentStage != len(r.StageSummaries) {
		return fmt.Errorf("invalid local runner stages")
	}
	var totals Metrics
	for index, stage := range r.StageSummaries {
		if stage.Stage != index+1 || stage.Concurrency != config.Workload.ConcurrencySteps[index] || !validRunnerMetrics(stage.Metrics, config.Workload.MaxRequests) || stage.SLOStatus != sloStatus(stage.Metrics, config.SLO) {
			return fmt.Errorf("invalid local runner stage metrics")
		}
		totals.RequestCount += stage.RequestCount
		totals.CompletedCount += stage.CompletedCount
		totals.SuccessCount += stage.SuccessCount
		totals.ErrorCount += stage.ErrorCount
		totals.DurationSeconds += stage.DurationSeconds
	}
	if totals.RequestCount != r.Metrics.RequestCount || totals.CompletedCount != r.Metrics.CompletedCount || totals.SuccessCount != r.Metrics.SuccessCount || totals.ErrorCount != r.Metrics.ErrorCount || totals.DurationSeconds > r.Metrics.DurationSeconds+0.05 {
		return fmt.Errorf("local runner totals do not match stage metrics")
	}
	if r.Status == "succeeded" && (r.Metrics.CompletedCount != r.Metrics.RequestCount || r.Metrics.RequestCount == 0 || len(r.StageSummaries) < len(config.Workload.ConcurrencySteps) && r.Metrics.RequestCount < config.Workload.MaxRequests) {
		return fmt.Errorf("successful local runner result is incomplete")
	}
	if r.Status == "failed" && !isRunnerErrorCode(r.ErrorCode) {
		return fmt.Errorf("invalid local runner error code")
	}
	if r.Status == "canceled" && r.ErrorCode != "RUN_CANCELED" {
		return fmt.Errorf("invalid local runner cancellation code")
	}
	if r.Status == "succeeded" && r.ErrorCode != "" {
		return fmt.Errorf("successful local runner result must not contain an error")
	}
	return nil
}

func validateLocalConfig(ctx context.Context, config Config, policy EndpointPolicy) error {
	if config.Target.Type != "openai_compatible" || config.Target.Model == "" || len(config.Target.Model) > 120 {
		return errors.New("LOCAL_RUNNER_TARGET_REQUIRED")
	}
	// Validate workload bounds separately so endpoint resolution respects the task context.
	common := config
	common.Target = Target{Type: "mock", Model: "mock-llm"}
	if err := common.validate(); err != nil {
		return err
	}
	_, err := validateEndpoint(ctx, config.Target.Endpoint, policy)
	return err
}

func validRunnerMetrics(m Metrics, maxRequests int) bool {
	if m.RequestCount < 0 || m.RequestCount > maxRequests || m.CompletedCount < 0 || m.CompletedCount > m.RequestCount || m.SuccessCount < 0 || m.ErrorCount < 0 || m.SuccessCount > m.CompletedCount || m.ErrorCount != m.CompletedCount-m.SuccessCount {
		return false
	}
	for _, value := range []float64{m.P50E2EMS, m.P95E2EMS, m.ErrorRate, m.ThroughputRPS, m.DurationSeconds} {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	if m.P50E2EMS > m.P95E2EMS || m.ErrorRate > 1 || m.P95E2EMS > m.DurationSeconds*1000+50 {
		return false
	}
	if m.CompletedCount == 0 {
		return m.P50E2EMS == 0 && m.P95E2EMS == 0 && m.ErrorRate == 0 && m.ThroughputRPS == 0
	}
	if m.DurationSeconds <= 0 {
		return false
	}
	return nearlyEqual(m.ErrorRate, float64(m.ErrorCount)/float64(m.CompletedCount)) && nearlyEqual(m.ThroughputRPS, float64(m.CompletedCount)/m.DurationSeconds)
}

func nearlyEqual(a, b float64) bool { return math.Abs(a-b) <= 1e-6*math.Max(1, math.Abs(b)) }

func isRunnerErrorCode(code string) bool {
	switch code {
	case "RUN_CANCELED", "STORAGE_WRITE_FAILED", ErrEndpointInvalid, ErrSSRFBlocked, "TARGET_AUTH_REQUIRED", ErrAuthFailed, ErrRateLimited, ErrHTTP4xx, ErrHTTP5xx, ErrRedirectBlocked, ErrTargetTimeout, ErrTargetCanceled, ErrConnectionFailed, ErrResponseInvalid, ErrResponseRead:
		return true
	default:
		return false
	}
}
