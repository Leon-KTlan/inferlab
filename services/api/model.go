package main

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

type Target struct {
	Type     string `json:"type"`
	Endpoint string `json:"endpoint,omitempty"`
	Model    string `json:"model"`
}

type Workload struct {
	ConcurrencySteps []int   `json:"concurrency_steps"`
	StageSeconds     int     `json:"stage_seconds"`
	MaxRequests      int     `json:"max_requests"`
	MaxRPS           float64 `json:"max_rps"`
	TimeoutMS        int     `json:"timeout_ms"`
}

type SLO struct {
	P95E2EMS     float64 `json:"p95_e2e_ms"`
	ErrorRateMax float64 `json:"error_rate_max"`
}

type Config struct {
	Name     string   `json:"name"`
	Target   Target   `json:"target"`
	Workload Workload `json:"workload"`
	SLO      SLO      `json:"slo"`
}

func (c *Config) validate() error {
	return c.validateWithEndpointPolicy(EndpointPolicy{})
}

func (c *Config) validateWithEndpointPolicy(policy EndpointPolicy) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || len(c.Name) > 120 {
		return errors.New("name must contain 1 to 120 bytes")
	}
	switch c.Target.Type {
	case "mock":
		if c.Target.Endpoint != "" {
			return errors.New("mock targets must not provide an endpoint")
		}
	case "openai_compatible":
		if _, err := validateEndpoint(context.Background(), c.Target.Endpoint, policy); err != nil {
			return err
		}
	default:
		return errors.New("target type must be mock or openai_compatible")
	}
	if c.Target.Model == "" {
		if c.Target.Type == "mock" {
			c.Target.Model = "mock-llm"
		} else {
			return errors.New("model is required for openai_compatible targets")
		}
	}
	if len(c.Target.Model) > 120 {
		return errors.New("model must be at most 120 bytes")
	}
	w := c.Workload
	if len(w.ConcurrencySteps) == 0 || len(w.ConcurrencySteps) > 8 {
		return errors.New("use 1 to 8 concurrency steps")
	}
	previous := 0
	for _, step := range w.ConcurrencySteps {
		if step <= previous || step > 32 {
			return errors.New("concurrency steps must increase strictly, between 1 and 32")
		}
		previous = step
	}
	if w.StageSeconds < 1 || w.StageSeconds > 30 || w.StageSeconds*len(w.ConcurrencySteps) > 120 {
		return errors.New("stages must last 1 to 30 seconds, with a total of at most 120 seconds")
	}
	if w.MaxRequests < 1 || w.MaxRequests > 1000 {
		return errors.New("max_requests must be between 1 and 1000")
	}
	if math.IsNaN(w.MaxRPS) || math.IsInf(w.MaxRPS, 0) || w.MaxRPS < 1 || w.MaxRPS > 50 {
		return errors.New("max_rps must be between 1 and 50")
	}
	if w.TimeoutMS < 10 || w.TimeoutMS > 10000 {
		return errors.New("timeout_ms must be between 10 and 10000")
	}
	if c.SLO.P95E2EMS <= 0 || c.SLO.P95E2EMS > 60000 || math.IsNaN(c.SLO.P95E2EMS) || math.IsInf(c.SLO.P95E2EMS, 0) || c.SLO.ErrorRateMax < 0 || c.SLO.ErrorRateMax > 1 || math.IsNaN(c.SLO.ErrorRateMax) || math.IsInf(c.SLO.ErrorRateMax, 0) {
		return errors.New("SLO requires positive p95_e2e_ms up to 60000 and error_rate_max between 0 and 1")
	}
	return nil
}

type Experiment struct {
	ExperimentID string `json:"experiment_id"`
	Config
	CreatedAt time.Time `json:"created_at"`
}

type Metrics struct {
	RequestCount    int     `json:"request_count"`
	CompletedCount  int     `json:"completed_count"`
	SuccessCount    int     `json:"success_count"`
	ErrorCount      int     `json:"error_count"`
	P50E2EMS        float64 `json:"p50_e2e_ms"`
	P95E2EMS        float64 `json:"p95_e2e_ms"`
	ErrorRate       float64 `json:"error_rate"`
	ThroughputRPS   float64 `json:"throughput_rps"`
	DurationSeconds float64 `json:"duration_seconds"`
}

type StageSummary struct {
	Stage       int `json:"stage"`
	Concurrency int `json:"concurrency"`
	Metrics
	SLOStatus string `json:"slo_status"`
}

type Run struct {
	Execution       *ExecutionSnapshot `json:"execution,omitempty"`
	RunID           string             `json:"run_id"`
	ExperimentID    string             `json:"experiment_id"`
	Status          string             `json:"status"`
	SourceType      string             `json:"source_type"`
	ResultKind      string             `json:"result_kind"`
	SoftwareVersion string             `json:"software_version"`
	Config          Config             `json:"config"`
	Metrics
	CurrentStage   int                 `json:"current_stage"`
	StageSummaries []StageSummary      `json:"stage_summaries"`
	CreatedAt      time.Time           `json:"created_at"`
	StartedAt      *time.Time          `json:"started_at"`
	FinishedAt     *time.Time          `json:"finished_at"`
	ErrorCode      string              `json:"error_code,omitempty"`
	Diagnostics    []RequestDiagnostic `json:"diagnostics,omitempty"`
}

func (r Run) terminal() bool {
	return r.Status == "succeeded" || r.Status == "failed" || r.Status == "canceled"
}

type Sample struct {
	Stage     int       `json:"stage"`
	StartedAt time.Time `json:"started_at"`
	LatencyMS float64   `json:"latency_ms"`
	ErrorCode string    `json:"error_code,omitempty"`
}

type Event struct {
	ID    int64     `json:"id"`
	RunID string    `json:"run_id"`
	Type  string    `json:"type"`
	Stage int       `json:"stage"`
	At    time.Time `json:"at"`
}

func metrics(samples []Sample, sent int, elapsed time.Duration) Metrics {
	m := Metrics{RequestCount: sent, CompletedCount: len(samples), DurationSeconds: elapsed.Seconds()}
	latencies := make([]float64, len(samples))
	for i, sample := range samples {
		latencies[i] = sample.LatencyMS
		if sample.ErrorCode != "" {
			m.ErrorCount++
		}
	}
	m.SuccessCount = m.CompletedCount - m.ErrorCount
	if len(samples) > 0 {
		m.ErrorRate = float64(m.ErrorCount) / float64(len(samples))
		sort.Float64s(latencies)
		m.P50E2EMS = latencies[int(math.Ceil(float64(len(samples))*.50))-1]
		m.P95E2EMS = latencies[int(math.Ceil(float64(len(samples))*.95))-1]
	}
	if elapsed > 0 {
		m.ThroughputRPS = float64(len(samples)) / elapsed.Seconds()
	}
	return m
}

func sloStatus(m Metrics, slo SLO) string {
	if m.CompletedCount < 20 {
		return "insufficient_data"
	}
	if m.P95E2EMS > slo.P95E2EMS || m.ErrorRate > slo.ErrorRateMax {
		return "fail"
	}
	return "pass"
}
