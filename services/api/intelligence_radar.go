package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
)

const intelligenceScoringVersion = "binary-v1"

type IntelligenceRadarGroup struct {
	ID            string   `json:"id"`
	Model         string   `json:"model"`
	SourceType    string   `json:"source_type"`
	EndpointLabel string   `json:"endpoint_label"`
	SuiteVersion  string   `json:"suite_version"`
	Repetitions   int      `json:"repetitions"`
	Runs          []Run    `json:"runs"`
	LatestRunID   string   `json:"latest_run_id"`
	BaselineRunID string   `json:"baseline_run_id,omitempty"`
	BaselineScore *float64 `json:"baseline_score,omitempty"`
	Delta         *float64 `json:"delta,omitempty"`
	Signal        string   `json:"signal"`
}

func intelligenceRadar(runs []Run) []IntelligenceRadarGroup {
	groups := []IntelligenceRadarGroup{}
	indices := map[string]int{}
	// Snapshot input order is not required; latest means newest creation time.
	ordered := append([]Run(nil), runs...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].CreatedAt.After(ordered[j].CreatedAt) })
	for _, run := range ordered {
		settings := run.Config.Intelligence
		if settings == nil {
			continue
		}
		key, _ := json.Marshal([]any{run.SourceType, run.Config.Target.Model, run.Config.Target.Endpoint, settings.SuiteVersion, settings.Repetitions, intelligenceScoringVersion})
		digest := sha256.Sum256(key)
		id := hex.EncodeToString(digest[:12])
		index, ok := indices[id]
		if !ok {
			label := "本地 Mock"
			if endpoint, err := url.Parse(run.Config.Target.Endpoint); err == nil && run.SourceType != "mock" {
				label = endpoint.Host + endpoint.EscapedPath()
			}
			index = len(groups)
			indices[id] = index
			groups = append(groups, IntelligenceRadarGroup{ID: id, Model: run.Config.Target.Model, SourceType: run.SourceType, EndpointLabel: label, SuiteVersion: settings.SuiteVersion, Repetitions: settings.Repetitions, Runs: []Run{}, LatestRunID: run.RunID, Signal: "insufficient_data"})
		}
		groups[index].Runs = append(groups[index].Runs, run)
	}
	for i := range groups {
		group := &groups[i]
		latest := group.Runs[0]
		if !intelligenceComparable(latest) {
			continue
		}
		history := []Run{}
		for _, run := range group.Runs[1:] {
			if intelligenceComparable(run) {
				history = append(history, run)
				if len(history) == 3 {
					break
				}
			}
		}
		if len(history) < 3 {
			continue
		}
		baseline := (history[0].IntelligenceReport.Score + history[1].IntelligenceReport.Score + history[2].IntelligenceReport.Score) / 3
		delta := latest.IntelligenceReport.Score - baseline
		group.BaselineRunID = history[0].RunID
		group.BaselineScore = &baseline
		group.Delta = &delta
		group.Signal = "stable"
		if delta <= -15 {
			group.Signal = "review"
		}
	}
	return groups
}

func intelligenceComparable(run Run) bool {
	report := run.IntelligenceReport
	return run.Status == "succeeded" && run.ErrorCode == "" && report != nil && report.Complete && report.ErrorCount == 0
}

func (a *App) getIntelligenceRadar(w http.ResponseWriter, r *http.Request) {
	runs, err := a.store.intelligenceRuns(a.owner(w, r))
	if err != nil {
		a.readError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"groups": intelligenceRadar(runs)})
}
