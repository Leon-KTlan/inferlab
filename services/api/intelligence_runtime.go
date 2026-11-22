package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

type intelligenceRuntime struct {
	store    *Store
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
	wg       sync.WaitGroup
	closed   bool
	interval time.Duration
}

func newIntelligenceRuntime(store *Store) *intelligenceRuntime {
	return &intelligenceRuntime{store: store, cancels: map[string]context.CancelFunc{}, interval: time.Second}
}

func (runtime *intelligenceRuntime) start(owner string, experiment Experiment, id string, createExperiment bool) (Run, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		return Run{}, errors.New("intelligence runtime closed")
	}
	report := summarizeIntelligence(*experiment.Intelligence, nil)
	run := Run{RunID: id, ExperimentID: experiment.ExperimentID, Status: "queued", SourceType: experiment.Target.Type, ResultKind: "measured", SoftwareVersion: "0.1.0", Config: experiment.Config, CreatedAt: time.Now().UTC(), StageSummaries: []StageSummary{}, IntelligenceReport: &report}
	if run.SourceType == "mock" {
		run.ResultKind = "demo"
	}
	var newExperiment *Experiment
	if createExperiment {
		newExperiment = &experiment
	}
	if err := runtime.store.reserveRunAndExperiment(owner, run, newExperiment); err != nil {
		return Run{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime.cancels[id] = cancel
	runtime.wg.Add(1)
	go func() {
		defer runtime.wg.Done()
		defer func() { cancel(); runtime.mu.Lock(); delete(runtime.cancels, id); runtime.mu.Unlock() }()
		if run.SourceType == "mock" {
			runtime.executeMock(ctx, owner, run)
		} else {
			runtime.expireTask(ctx, owner, run)
		}
	}()
	return run, nil
}

func (runtime *intelligenceRuntime) cancel(id string) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if cancel := runtime.cancels[id]; cancel != nil {
		cancel()
	}
}

func (runtime *intelligenceRuntime) Close() {
	runtime.mu.Lock()
	runtime.closed = true
	for _, cancel := range runtime.cancels {
		cancel()
	}
	runtime.mu.Unlock()
	runtime.wg.Wait()
}

func (runtime *intelligenceRuntime) expireTask(ctx context.Context, owner string, run Run) {
	timer := time.NewTimer(time.Until(run.CreatedAt.Add(intelligenceTaskTTL)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	now := time.Now().UTC()
	run.Status = "failed"
	run.ErrorCode = "TASK_EXPIRED"
	run.FinishedAt = &now
	updateIntelligenceCounts(&run)
	if err := runtime.store.finishLocalRun(owner, run, "intelligence_expired"); err != nil && !errors.Is(err, errRunAlreadyFinished) {
		runtime.store.taskFailures.Add(1)
	}
}

func (runtime *intelligenceRuntime) executeMock(parent context.Context, owner string, run Run) {
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	now := time.Now().UTC()
	run.Status = "running"
	run.StartedAt = &now
	if err := runtime.store.saveRun(run, nil, "intelligence_started"); err != nil {
		runtime.failMock(owner, run)
		return
	}
	outcomes := []IntelligenceOutcome{}
	for repetition := 1; repetition <= run.Config.Intelligence.Repetitions; repetition++ {
		for _, probe := range intelligenceProbes() {
			timer := time.NewTimer(runtime.interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				now = time.Now().UTC()
				run.Status = "canceled"
				run.ErrorCode = "RUN_CANCELED"
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					run.Status = "failed"
					run.ErrorCode = "RUN_TIMEOUT"
				}
				run.FinishedAt = &now
				updateIntelligenceCounts(&run)
				if err := runtime.store.finishLocalRun(owner, run, "intelligence_"+run.Status); err != nil && !errors.Is(err, errRunAlreadyFinished) {
					runtime.store.taskFailures.Add(1)
				}
				return
			case <-timer.C:
			}
			started := time.Now()
			passed := gradeIntelligence(probe.ID, intelligenceMockAnswer(probe, run.Config.Intelligence.MockProfile))
			outcomes = append(outcomes, IntelligenceOutcome{ProbeID: probe.ID, Repetition: repetition, Passed: passed, LatencyMS: float64(time.Since(started)) / float64(time.Millisecond)})
			report := summarizeIntelligence(*run.Config.Intelligence, outcomes)
			run.IntelligenceReport = &report
			updateIntelligenceCounts(&run)
			if err := runtime.store.saveRun(run, nil, "intelligence_progress"); err != nil {
				runtime.failMock(owner, run)
				return
			}
		}
	}
	now = time.Now().UTC()
	run.Status = "succeeded"
	run.FinishedAt = &now
	updateIntelligenceCounts(&run)
	if err := runtime.store.finishLocalRun(owner, run, "intelligence_succeeded"); err != nil && !errors.Is(err, errRunAlreadyFinished) {
		runtime.store.taskFailures.Add(1)
	}
}

func (runtime *intelligenceRuntime) failMock(owner string, run Run) {
	now := time.Now().UTC()
	run.Status = "failed"
	run.ErrorCode = "STORAGE_WRITE_FAILED"
	run.FinishedAt = &now
	updateIntelligenceCounts(&run)
	if err := runtime.store.finishLocalRun(owner, run, "intelligence_failed"); err != nil {
		runtime.store.taskFailures.Add(1)
	}
}
