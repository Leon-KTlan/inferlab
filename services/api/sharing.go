package main

import (
	"encoding/json"
	"errors"
	"net/http"
)

// This DTO is the entire public contract. Never embed Run, Config, or diagnostics.
type PublicReport struct {
	Status     string         `json:"status"`
	SourceType string         `json:"source_type"`
	ResultKind string         `json:"result_kind"`
	Metrics    Metrics        `json:"metrics"`
	Stages     []StageSummary `json:"stages"`
	SLO        SLO            `json:"slo"`
	SLOStatus  string         `json:"slo_status"`
}

func (a *App) createShare(w http.ResponseWriter, r *http.Request) {
	token, err := a.store.share(a.owner(w, r), r.PathValue("id"))
	if errors.Is(err, errReportNotFinished) {
		writeError(w, 409, "RUN_NOT_FINISHED", "only terminal reports can be shared")
		return
	}
	if err != nil {
		a.readError(w, err)
		return
	}
	writeJSON(w, 201, map[string]string{"token": token, "path": "/share/" + token})
}

func (a *App) getShare(w http.ResponseWriter, r *http.Request) {
	var data []byte
	err := a.store.db.QueryRow("SELECT r.data FROM report_shares s JOIN runs r ON r.id=s.run_id WHERE s.token=? AND r.status NOT IN ('queued','running')", r.PathValue("token")).Scan(&data)
	if err != nil {
		a.readError(w, err)
		return
	}
	var run Run
	if err = json.Unmarshal(data, &run); err != nil {
		a.readError(w, err)
		return
	}
	status := "pass"
	for _, stage := range run.StageSummaries {
		if stage.SLOStatus == "fail" {
			status = "fail"
		} else if stage.SLOStatus == "insufficient_data" && status != "fail" {
			status = "insufficient_data"
		}
	}
	if status != "fail" && (run.Status != "succeeded" || len(run.StageSummaries) < len(run.Config.Workload.ConcurrencySteps)) {
		status = "insufficient_data"
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	writeJSON(w, 200, PublicReport{Status: run.Status, SourceType: run.SourceType, ResultKind: run.ResultKind, Metrics: run.Metrics, Stages: run.StageSummaries, SLO: run.Config.SLO, SLOStatus: status})
}

func (a *App) revokeShare(w http.ResponseWriter, r *http.Request) {
	owner := a.owner(w, r)
	if err := a.store.revokeShare(owner, r.PathValue("id")); err != nil {
		a.readError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"revoked": true})
}

func (a *App) deleteRun(w http.ResponseWriter, r *http.Request)        { a.deleteRecord(w, r, false) }
func (a *App) deleteExperiment(w http.ResponseWriter, r *http.Request) { a.deleteRecord(w, r, true) }
func (a *App) deleteRecord(w http.ResponseWriter, r *http.Request, experiment bool) {
	err := a.store.deleteRecords(a.owner(w, r), r.PathValue("id"), experiment)
	if errors.Is(err, errActiveDelete) {
		writeError(w, 409, "RUN_ACTIVE", "cancel active runs and wait for completion before deleting")
		return
	}
	if err != nil {
		a.readError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}
