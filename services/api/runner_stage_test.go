package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stageTestAdapter func(context.Context) string

func (a stageTestAdapter) Request(ctx context.Context) string { return a(ctx) }

func TestRunnerStageBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel bool
	}{
		{name: "completion after send window"},
		{name: "cancellation during drain", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := openStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.db.Close()
			started, release := make(chan struct{}, 2), make(chan struct{})
			adapter := stageTestAdapter(func(ctx context.Context) string {
				started <- struct{}{}
				select {
				case <-release:
					return ""
				case <-ctx.Done():
					if errors.Is(ctx.Err(), context.DeadlineExceeded) {
						return "REQUEST_TIMEOUT"
					}
					return "REQUEST_CANCELED"
				}
			})
			runner := newRunner(store, adapter)
			defer runner.Close()
			cfg := testConfig()
			cfg.Workload = Workload{ConcurrencySteps: []int{1}, StageSeconds: 1, MaxRequests: 2, MaxRPS: 50, TimeoutMS: 3000}
			exp := Experiment{ExperimentID: "stage-boundary", Config: cfg}
			if err := store.createExperiment("owner", exp); err != nil {
				t.Fatal(err)
			}
			run, err := runner.start("owner", exp)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("request never started")
			}
			// The adapter withholds its response until the stage has stopped sending.
			time.Sleep(1100 * time.Millisecond)
			if tc.cancel {
				runner.cancel("owner", run.RunID)
			} else {
				close(release)
			}
			done := make(chan struct{})
			go func() {
				runner.wg.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("run did not finish while draining")
			}
			saved, err := store.run("owner", run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if saved.SoftwareVersion != "0.1.1" || saved.RequestCount != 1 || saved.CompletedCount != 1 || len(saved.StageSummaries) != 1 || saved.StageSummaries[0].DurationSeconds < 1 {
				t.Fatalf("unexpected stage counts or observation window: %+v", saved)
			}
			if tc.cancel {
				if saved.Status != "canceled" || saved.ErrorCode != "RUN_CANCELED" || saved.ErrorCount != 1 {
					t.Fatalf("run did not cancel while draining: %+v", saved)
				}
			} else if saved.Status != "succeeded" || saved.SuccessCount != 1 || saved.ErrorCount != 0 {
				t.Fatalf("stage deadline changed the request result: %+v", saved)
			}
		})
	}
}
