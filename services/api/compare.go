package main

import (
	"net/http"
	"slices"
)

type MetricDelta struct {
	P95E2EMS      float64 `json:"p95_e2e_ms"`
	ErrorRate     float64 `json:"error_rate"`
	ThroughputRPS float64 `json:"throughput_rps"`
}

type StageComparison struct {
	Stage       int          `json:"stage"`
	Concurrency int          `json:"concurrency"`
	Baseline    StageSummary `json:"baseline"`
	Compare     StageSummary `json:"compare"`
	Delta       MetricDelta  `json:"delta"`
}

type OverallComparison struct {
	Baseline Metrics     `json:"baseline"`
	Compare  Metrics     `json:"compare"`
	Delta    MetricDelta `json:"delta"`
}

type Comparison struct {
	BaselineRunID string            `json:"baseline_run_id"`
	CompareRunID  string            `json:"compare_run_id"`
	Comparable    bool              `json:"comparable"`
	Stages        []StageComparison `json:"stages"`
	Overall       OverallComparison `json:"overall"`
	Notes         []string          `json:"notes"`
}

func metricDelta(baseline, compare Metrics) MetricDelta {
	return MetricDelta{
		P95E2EMS:      compare.P95E2EMS - baseline.P95E2EMS,
		ErrorRate:     compare.ErrorRate - baseline.ErrorRate,
		ThroughputRPS: compare.ThroughputRPS - baseline.ThroughputRPS,
	}
}

func comparisonIssue(baseline, compare Run) (string, string) {
	if !baseline.terminal() || !compare.terminal() {
		return "RUN_NOT_FINISHED", "both runs must finish before comparison"
	}
	if baseline.Status != "succeeded" || compare.Status != "succeeded" {
		return "RUN_NOT_SUCCEEDED", "both runs must succeed; canceled or failed runs cannot establish a baseline"
	}
	// Names and experiment IDs identify templates, not the workload that was measured.
	bw, cw := baseline.Config.Workload, compare.Config.Workload
	if baseline.SourceType != "mock" || compare.SourceType != "mock" || baseline.ResultKind != compare.ResultKind || baseline.SoftwareVersion != compare.SoftwareVersion ||
		baseline.Config.Target != compare.Config.Target || baseline.Config.SLO != compare.Config.SLO ||
		!slices.Equal(bw.ConcurrencySteps, cw.ConcurrencySteps) || bw.StageSeconds != cw.StageSeconds || bw.MaxRequests != cw.MaxRequests || bw.MaxRPS != cw.MaxRPS || bw.TimeoutMS != cw.TimeoutMS {
		return "CONFIG_MISMATCH", "source, software version, target, workload and SLO snapshots must match"
	}
	for _, run := range []Run{baseline, compare} {
		if len(run.StageSummaries) != len(run.Config.Workload.ConcurrencySteps) || len(run.StageSummaries) == 0 {
			return "INCOMPLETE_STAGES", "both runs must cover all configured concurrency stages"
		}
		completed := 0
		for index, stage := range run.StageSummaries {
			if stage.Stage != index+1 || stage.Concurrency != run.Config.Workload.ConcurrencySteps[index] || stage.RequestCount != stage.CompletedCount || stage.DurationSeconds < float64(run.Config.Workload.StageSeconds) {
				return "INCOMPLETE_STAGES", "stage measurements must be complete and match the workload snapshot"
			}
			if stage.CompletedCount < 20 {
				return "INSUFFICIENT_SAMPLES", "each stage in both runs requires at least 20 completed samples"
			}
			completed += stage.CompletedCount
		}
		if run.CompletedCount != completed || run.RequestCount != completed || run.DurationSeconds <= 0 || run.StartedAt == nil || run.FinishedAt == nil {
			return "INCOMPLETE_STAGES", "run totals and observation windows must match complete stage measurements"
		}
	}
	return "", ""
}

func (a *App) compareReports(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()["baseline_run_id"]
	if len(values) != 1 || values[0] == "" {
		writeError(w, http.StatusBadRequest, "INVALID_COMPARISON", "provide exactly one baseline_run_id")
		return
	}
	owner := a.owner(w, r)
	compare, err := a.store.run(owner, r.PathValue("id"))
	if err != nil {
		a.readError(w, err)
		return
	}
	baseline, err := a.store.run(owner, values[0])
	if err != nil {
		a.readError(w, err)
		return
	}
	if baseline.RunID == compare.RunID {
		writeError(w, http.StatusBadRequest, "INVALID_COMPARISON", "select two distinct runs")
		return
	}
	if code, message := comparisonIssue(baseline, compare); code != "" {
		writeError(w, http.StatusConflict, code, message)
		return
	}
	result := Comparison{
		BaselineRunID: baseline.RunID,
		CompareRunID:  compare.RunID,
		Comparable:    true,
		Stages:        make([]StageComparison, len(compare.StageSummaries)),
		Overall:       OverallComparison{Baseline: baseline.Metrics, Compare: compare.Metrics, Delta: metricDelta(baseline.Metrics, compare.Metrics)},
		Notes: []string{
			"两次运行的来源、软件版本、目标、负载和 SLO 快照一致；所有阶段均完成且每阶段至少20个样本。",
			"差值为候选减基线；P95 单位为毫秒，错误率为0到1比例的差，吞吐单位为请求/秒。总体 P95 直接使用各 Run 的总体原始采样统计，不平均阶段 P95。",
			"这是两次 Mock 采样的观测差异，不代表真实模型服务或版本优化结论。Mock 共享进程内服务，其他 Run、运行时段与主机负载可能影响结果；20个样本只满足展示门槛，不代表统计显著。",
		},
	}
	for index, stage := range compare.StageSummaries {
		base := baseline.StageSummaries[index]
		result.Stages[index] = StageComparison{Stage: stage.Stage, Concurrency: stage.Concurrency, Baseline: base, Compare: stage, Delta: metricDelta(base.Metrics, stage.Metrics)}
	}
	writeJSON(w, http.StatusOK, result)
}
