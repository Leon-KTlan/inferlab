package main

import "time"

const mockScenarioHeader = "X-InferLab-Mock-Scenario"

func mockScenarioDelay(id string) (time.Duration, bool) {
	switch id {
	case "baseline-v1":
		return 160 * time.Millisecond, true
	case "slow-v1":
		return 420 * time.Millisecond, true
	default:
		return 0, false
	}
}
