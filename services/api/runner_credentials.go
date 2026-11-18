package main

import (
	"net/http"
	"sync"
	"time"
)

type runnerCredential struct {
	key     string
	expires time.Time
	timer   *time.Timer
}

type runnerCredentials struct {
	mu      sync.Mutex
	entries map[string]runnerCredential
}

func (c *runnerCredentials) put(id, key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= 64 {
		return false
	}
	if c.entries == nil {
		c.entries = make(map[string]runnerCredential)
	}
	entry := runnerCredential{key: key, expires: time.Now().Add(5 * time.Minute)}
	entry.timer = time.AfterFunc(5*time.Minute, func() { c.remove(id) })
	c.entries[id] = entry
	return true
}

func (c *runnerCredentials) take(id string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[id]
	if !ok {
		return "", false
	}
	delete(c.entries, id)
	entry.timer.Stop()
	if !time.Now().Before(entry.expires) {
		return "", false
	}
	return entry.key, true
}

func (c *runnerCredentials) remove(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[id]; ok {
		entry.timer.Stop()
		delete(c.entries, id)
	}
}

func (c *runnerCredentials) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, entry := range c.entries {
		entry.timer.Stop()
		delete(c.entries, id)
	}
}

func (a *App) claimRunnerCredential(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.validRunnerToken(id, r.Header.Get("X-InferLab-Runner-Token")) {
		writeError(w, 403, "RUNNER_TOKEN_INVALID", "valid task token required")
		return
	}
	owner, err := a.store.runOwner(id)
	if err != nil {
		a.readError(w, err)
		return
	}
	run, err := a.store.run(owner, id)
	if err != nil {
		a.readError(w, err)
		return
	}
	if run.terminal() {
		a.credentials.remove(id)
		writeError(w, 409, "RUN_ALREADY_FINISHED", "run is already terminal")
		return
	}
	var input struct {
		Target Target `json:"target"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Target != run.Config.Target {
		writeError(w, 400, "TARGET_MISMATCH", "task target does not match run")
		return
	}
	key, ok := a.credentials.take(id)
	if !ok {
		writeError(w, 410, "CREDENTIAL_UNAVAILABLE", "credential expired or already claimed; create a new task")
		return
	}
	writeJSON(w, 200, map[string]any{"api_key": key, "target": run.Config.Target})
}
