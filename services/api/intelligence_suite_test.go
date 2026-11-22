package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIntelligenceSuiteProbes(t *testing.T) {
	probes := intelligenceProbes()
	if len(probes) != 10 {
		t.Fatalf("got %d probes, want 10", len(probes))
	}
	counts := make(map[string]int)
	ids := make(map[string]bool)
	for _, probe := range probes {
		if ids[probe.ID] || probe.ID == "" || probe.Title == "" || probe.Prompt == "" || probe.Expected == "" {
			t.Fatalf("invalid or repeated probe: %s", probe.ID)
		}
		ids[probe.ID] = true
		counts[probe.Dimension]++
		if !gradeIntelligence(probe.ID, probe.Expected) || !gradeIntelligence(probe.ID, " \n"+probe.Expected+"\t ") {
			t.Errorf("expected answer rejected: %s", probe.ID)
		}
		for _, wrong := range []string{"", "INCORRECT", "答案是" + probe.Expected, probe.Expected + " because", "```\n" + probe.Expected + "\n```"} {
			if gradeIntelligence(probe.ID, wrong) {
				t.Errorf("nonconforming answer accepted: %s", probe.ID)
			}
		}
	}
	if len(counts) != 5 {
		t.Fatalf("got %d dimensions, want 5", len(counts))
	}
	for _, dimension := range intelligenceDimensions() {
		if counts[dimension.ID] != 2 || dimension.Label == "" {
			t.Errorf("dimension %s has %d probes", dimension.ID, counts[dimension.ID])
		}
	}
	if gradeIntelligence("missing", "CAB") {
		t.Error("unknown probe accepted")
	}
}

func TestIntelligenceSuiteStrictJSON(t *testing.T) {
	for _, test := range []struct {
		name    string
		probeID string
		content string
		want    bool
	}{
		{"field order and whitespace", "structured-1", ` { "count" : 3, "color" : "teal" } `, true},
		{"escaped string", "structured-1", `{"color":"\u0074eal","count":3}`, true},
		{"typed array and boolean", "structured-2", `{"ready":true,"ids":["B2", "D4"]}`, true},
		{"wrong color", "structured-1", `{"color":"blue","count":3}`, false},
		{"unknown field", "structured-1", `{"color":"teal","count":3,"extra":true}`, false},
		{"case differs", "structured-1", `{"Color":"teal","count":3}`, false},
		{"missing field", "structured-1", `{"color":"teal"}`, false},
		{"string integer", "structured-1", `{"color":"teal","count":"3"}`, false},
		{"fractional integer", "structured-1", `{"color":"teal","count":3.0}`, false},
		{"null", "structured-1", `{"color":"teal","count":null}`, false},
		{"duplicate same value", "structured-1", `{"color":"teal","count":3,"count":3}`, false},
		{"duplicate overwritten", "structured-1", `{"color":"blue","color":"teal","count":3}`, false},
		{"duplicate escaped key", "structured-1", `{"color":"blue","\u0063olor":"teal","count":3}`, false},
		{"trailing JSON", "structured-1", `{"color":"teal","count":3}{}`, false},
		{"trailing text", "structured-1", `{"color":"teal","count":3} done`, false},
		{"truncated", "structured-1", `{"color":"teal","count":3`, false},
		{"invalid comma", "structured-1", `{"color":"teal","count":3,}`, false},
		{"wrong root", "structured-1", `[{"color":"teal","count":3}]`, false},
		{"swapped array", "structured-2", `{"ids":["D4","B2"],"ready":true}`, false},
		{"boolean string", "structured-2", `{"ids":["B2","D4"],"ready":"true"}`, false},
		{"extra array element", "structured-2", `{"ids":["B2","D4","E6"],"ready":true}`, false},
		{"nested duplicate", "structured-2", `{"ids":[{"a":1,"a":1}],"ready":true}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := gradeIntelligence(test.probeID, test.content); got != test.want {
				t.Fatalf("grade = %v, want %v", got, test.want)
			}
		})
	}
}

func intelligenceSuiteTestOutcomes(profile string, repetitions int) []IntelligenceOutcome {
	var outcomes []IntelligenceOutcome
	for _, probe := range intelligenceProbes() {
		for repetition := 1; repetition <= repetitions; repetition++ {
			outcomes = append(outcomes, IntelligenceOutcome{
				ProbeID: probe.ID, Repetition: repetition,
				Passed: gradeIntelligence(probe.ID, intelligenceMockAnswer(probe, profile)), LatencyMS: 12.5,
			})
		}
	}
	return outcomes
}

func TestIntelligenceSuiteMockProfiles(t *testing.T) {
	for _, repetitions := range []int{1, 2, 3} {
		for _, profile := range []string{"reference", "degraded"} {
			settings := IntelligenceSettings{SuiteVersion: intelligenceSuiteVersion, Repetitions: repetitions, MockProfile: profile}
			report := summarizeIntelligence(settings, intelligenceSuiteTestOutcomes(profile, repetitions))
			wantScore := 100.0
			wantPassed := 10 * repetitions
			if profile == "degraded" {
				wantScore, wantPassed = 40, 4*repetitions
			}
			if report.SuiteVersion != intelligenceSuiteVersion || report.Score != wantScore || report.Passed != wantPassed || report.Total != 10*repetitions || report.Completed != report.Total || report.ErrorCount != 0 || !report.Complete {
				t.Fatalf("profile %s, repetitions %d: unexpected report %+v", profile, repetitions, report)
			}
			for _, dimension := range report.Dimensions {
				wantDimensionScore := 100.0
				if profile == "degraded" && dimension.ID != "math" && dimension.ID != "context" {
					wantDimensionScore = 0
				}
				if dimension.Score != wantDimensionScore || dimension.Total != 2*repetitions || dimension.Completed != dimension.Total {
					t.Errorf("profile %s: unexpected dimension %+v", profile, dimension)
				}
			}
		}
	}
}

func TestIntelligenceSuiteSummaryIncompleteAndErrors(t *testing.T) {
	settings := IntelligenceSettings{SuiteVersion: intelligenceSuiteVersion, Repetitions: 2}
	outcomes := intelligenceSuiteTestOutcomes("reference", settings.Repetitions)
	outcomes[0].ErrorCode = "timeout"
	report := summarizeIntelligence(settings, outcomes[:19])
	if report.Score != 90 || report.Passed != 18 || report.Total != 20 || report.Completed != 19 || report.ErrorCount != 1 || report.Complete {
		t.Fatalf("unexpected incomplete report: %+v", report)
	}
	if !strings.Contains(report.Conclusion, "不可用于历史比较") || report.Dimensions[0].Score != 75 || report.Dimensions[4].Score != 75 || report.Dimensions[4].Completed != 3 {
		t.Fatalf("incomplete dimensions/conclusion incorrect: %+v", report)
	}
	if report.Outcomes[0].Passed || !outcomes[0].Passed {
		t.Fatal("transport error must fail without mutating the caller's outcomes")
	}
	report = summarizeIntelligence(settings, outcomes)
	if !report.Complete || report.Score != 95 || report.Completed != 20 || report.ErrorCount != 1 || !strings.Contains(report.Conclusion, "不参与历史基线比较") {
		t.Fatalf("completed transport failure is distinct from missing result: %+v", report)
	}
	outcomes[0].ErrorCode = ""
	outcomes[0].Passed = false
	report = summarizeIntelligence(settings, outcomes)
	if !report.Complete || report.Score != 95 || report.ErrorCount != 0 {
		t.Fatalf("wrong answer must not count as a transport failure: %+v", report)
	}
	empty := summarizeIntelligence(settings, nil)
	if empty.Score != 0 || empty.Total != 20 || empty.Completed != 0 || empty.Complete || len(empty.Outcomes) != 0 {
		t.Fatalf("unexpected empty report: %+v", empty)
	}
}

func TestIntelligenceSuiteSummaryIgnoresUnplannedSlots(t *testing.T) {
	settings := IntelligenceSettings{SuiteVersion: intelligenceSuiteVersion, Repetitions: 1}
	outcomes := intelligenceSuiteTestOutcomes("reference", 1)[:9]
	outcomes = append(outcomes, outcomes[0], IntelligenceOutcome{ProbeID: "unknown", Repetition: 1, Passed: true}, IntelligenceOutcome{ProbeID: "context-2", Repetition: 2, Passed: true}, IntelligenceOutcome{ProbeID: "context-2", Repetition: 0, Passed: true})
	report := summarizeIntelligence(settings, outcomes)
	if report.Score != 90 || report.Completed != 9 || report.Total != 10 || report.Complete || len(report.Outcomes) != 9 {
		t.Fatalf("unplanned or duplicate outcomes inflated report: %+v", report)
	}
}

func TestIntelligenceSuiteReportContainsOnlyOutcomes(t *testing.T) {
	report := summarizeIntelligence(IntelligenceSettings{SuiteVersion: intelligenceSuiteVersion, Repetitions: 1}, intelligenceSuiteTestOutcomes("reference", 1))
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"prompt":`, `"expected":`, `"content":`, `"answer":`} {
		if strings.Contains(string(encoded), key) {
			t.Fatalf("raw answer or prompt field persisted: %s", key)
		}
	}
}
