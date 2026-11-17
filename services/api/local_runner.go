package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RunnerTask is the credential-free handoff between the API and a local runner.
// The API key is deliberately absent and must stay in the local runner process.
type RunnerTask struct {
	TaskID         string `json:"task_id"`
	RunID          string `json:"run_id"`
	ExperimentID   string `json:"experiment_id"`
	SourceType     string `json:"source_type"`
	Config         Config `json:"config"`
	TaskToken      string `json:"task_token"`
	ResultEndpoint string `json:"result_endpoint"`
}

type RunnerResult struct {
	RunID          string         `json:"run_id"`
	Status         string         `json:"status"`
	Metrics        Metrics        `json:"metrics"`
	CurrentStage   int            `json:"current_stage"`
	StageSummaries []StageSummary `json:"stage_summaries"`
	StartedAt      *time.Time     `json:"started_at,omitempty"`
	FinishedAt     *time.Time     `json:"finished_at,omitempty"`
	ErrorCode      string         `json:"error_code,omitempty"`
}

func executeLocalTask(ctx context.Context, task RunnerTask, apiKey string) (RunnerResult, error) {
	adapter, err := newOpenAICompatibleAdapter(task.Config.Target, apiKey)
	if err != nil {
		return RunnerResult{}, err
	}
	return executeLocalTaskWithAdapter(ctx, task, adapter)
}

func executeLocalTaskWithAdapter(ctx context.Context, task RunnerTask, adapter Adapter) (RunnerResult, error) {
	if task.SourceType != "openai_compatible" || task.Config.Target.Type != "openai_compatible" {
		return RunnerResult{}, errors.New("LOCAL_RUNNER_TARGET_REQUIRED")
	}
	if err := task.Config.validateWithEndpointPolicy(EndpointPolicy{AllowLoopback: true}); err != nil {
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
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (r RunnerResult) validateFor(config Config) error {
	if r.Status != "succeeded" && r.Status != "failed" && r.Status != "canceled" {
		return fmt.Errorf("invalid local runner status")
	}
	if r.Metrics.RequestCount < 0 || r.Metrics.RequestCount > config.Workload.MaxRequests || r.Metrics.CompletedCount < 0 || r.Metrics.CompletedCount > r.Metrics.RequestCount || r.Metrics.SuccessCount < 0 || r.Metrics.ErrorCount < 0 || r.Metrics.SuccessCount+r.Metrics.ErrorCount != r.Metrics.CompletedCount {
		return fmt.Errorf("invalid local runner metrics")
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

func isRunnerErrorCode(code string) bool {
	switch code {
	case "RUN_CANCELED", "STORAGE_WRITE_FAILED", ErrEndpointInvalid, ErrSSRFBlocked, "TARGET_AUTH_REQUIRED", ErrAuthFailed, ErrRateLimited, ErrHTTP4xx, ErrHTTP5xx, ErrRedirectBlocked, ErrTargetTimeout, ErrTargetCanceled, ErrConnectionFailed, ErrResponseInvalid, ErrResponseRead:
		return true
	default:
		return false
	}
}
