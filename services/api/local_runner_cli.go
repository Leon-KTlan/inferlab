package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Test-only policies are passed directly by tests; the CLI exposes no bypass flags.
type runnerCLIOptions struct {
	targetPolicy EndpointPolicy
	apiTLSConfig *tls.Config
}

func runRunnerCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runRunnerCLIWithOptions(ctx, args, stdout, stderr, runnerCLIOptions{})
}

func runRunnerCLIWithOptions(ctx context.Context, args []string, stdout, stderr io.Writer, options runnerCLIOptions) int {
	flags := flag.NewFlagSet("runner", flag.ContinueOnError)
	// flag errors can echo untrusted argument values, so only print our fixed usage.
	flags.SetOutput(io.Discard)
	taskPath := flags.String("task", "", "downloaded task JSON path")
	apiURL := flags.String("api-url", "", "trusted InferLab API origin")
	authorized := flags.Bool("authorize-target", false, "authorize the target in the task")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *taskPath == "" || *apiURL == "" {
		fmt.Fprintln(stderr, "RUNNER_ARGUMENT_INVALID: runner --task /path/task.json --api-url https://trusted-api.example --authorize-target")
		return 2
	}
	if !*authorized {
		fmt.Fprintln(stderr, "TARGET_AUTH_REQUIRED: explicitly provide --authorize-target after reviewing the task")
		return 2
	}
	apiOrigin, err := trustedAPIOrigin(*apiURL)
	if err != nil {
		fmt.Fprintln(stderr, "RUNNER_API_URL_INVALID: use an HTTPS origin or an HTTP loopback origin")
		return 2
	}
	task, err := readRunnerTask(*taskPath)
	if err != nil {
		fmt.Fprintln(stderr, "RUNNER_TASK_INVALID: cannot read a valid bounded task JSON file")
		return 2
	}
	validationCtx, validationCancel := context.WithTimeout(ctx, 10*time.Second)
	err = validateRunnerTask(validationCtx, task, options.targetPolicy)
	validationCancel()
	if err != nil {
		fmt.Fprintln(stderr, "RUNNER_TASK_INVALID: task identity, target, workload or result endpoint is invalid")
		return 2
	}
	// The downloaded endpoint never selects an origin or alters the canonical path.
	apiOrigin.Path = "/api/v1/runner-tasks/" + task.RunID + "/result"
	key := os.Getenv("OPENAI_API_KEY")
	if task.CredentialAvailable {
		key, err = claimLocalCredential(ctx, apiOrigin, task, options.apiTLSConfig)
		if err != nil {
			fmt.Fprintln(stderr, "CREDENTIAL_UNAVAILABLE: temporary Key could not be claimed; cancel the old task and create a new one")
			return 1
		}
	}
	result, executeCode := runValidatedTask(ctx, task, key, options.targetPolicy)
	if err := submitLocalResult(ctx, apiOrigin.String(), task.TaskToken, result, options.apiTLSConfig); err != nil {
		fmt.Fprintln(stderr, "RUNNER_RESULT_SUBMISSION_FAILED: result was not confirmed by the trusted API; do not rerun the target automatically")
		return 1
	}
	fmt.Fprintf(stdout, "Runner result accepted: %s (%d completed, %d errors)\n", result.Status, result.Metrics.CompletedCount, result.Metrics.ErrorCount)
	if executeCode != "" {
		fmt.Fprintln(stderr, executeCode)
	}
	if ctx.Err() != nil {
		return 130
	}
	if result.Status != "succeeded" {
		return 1
	}
	return 0
}

func trustedAPIOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(raw, "\\\r\n\t") {
		return nil, errors.New("invalid API origin")
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if !strings.EqualFold(u.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("HTTP API must use loopback")
		}
	}
	port, err := strconv.Atoi(portOrDefault(u))
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("invalid API port")
	}
	return u, nil
}

func portOrDefault(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func readRunnerTask(path string) (RunnerTask, error) {
	var task RunnerTask
	file, err := os.Open(path)
	if err != nil {
		return task, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return task, errors.New("invalid task file")
	}
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil || len(data) > 16<<10 {
		return task, errors.New("invalid task size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&task); err != nil {
		return task, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return task, errors.New("invalid task JSON")
	}
	return task, nil
}

func validRunnerID(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) <= len(prefix) || len(value) > 128 {
		return false
	}
	for _, c := range value[len(prefix):] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validateRunnerTask(ctx context.Context, task RunnerTask, policy EndpointPolicy) error {
	if !validRunnerID(task.RunID, "run_") || task.TaskID != task.RunID || !validRunnerID(task.ExperimentID, "exp_") || task.SourceType != "openai_compatible" || task.Config.Target.Type != "openai_compatible" || task.Config.Target.Model == "" || len(task.Config.Target.Model) > 120 {
		return errors.New("invalid task identity or target")
	}
	token, err := base64.RawURLEncoding.DecodeString(task.TaskToken)
	if err != nil || len(token) != 32 || task.ResultEndpoint != "/api/v1/runner-tasks/"+task.RunID+"/result" {
		return errors.New("invalid task submission binding")
	}
	return validateLocalConfig(ctx, task.Config, policy)
}

type classifiedAdapter struct {
	Adapter
	mu         sync.Mutex
	firstError string
}

func (a *classifiedAdapter) Request(ctx context.Context) string {
	code := a.Adapter.Request(ctx)
	if code != "" {
		a.mu.Lock()
		if a.firstError == "" {
			a.firstError = code
		}
		a.mu.Unlock()
	}
	return code
}

func runValidatedTask(ctx context.Context, task RunnerTask, key string, policy EndpointPolicy) (RunnerResult, string) {
	now := time.Now().UTC()
	failure := func(code string) (RunnerResult, string) {
		finished := time.Now().UTC()
		return RunnerResult{RunID: task.RunID, Status: "failed", StartedAt: &now, FinishedAt: &finished, StageSummaries: []StageSummary{}, ErrorCode: code}, code
	}
	bound := time.Duration(task.Config.Workload.StageSeconds*len(task.Config.Workload.ConcurrencySteps))*time.Second + 5*time.Second
	runCtx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	adapter, err := newOpenAICompatibleAdapterWithContext(runCtx, task.Config.Target, key, policy)
	if err != nil {
		code := ErrEndpointInvalid
		if strings.TrimSpace(key) == "" {
			code = "TARGET_AUTH_REQUIRED"
		} else if strings.Contains(err.Error(), ErrSSRFBlocked) {
			code = ErrSSRFBlocked
		}
		return failure(code)
	}
	defer adapter.Close()
	classified := &classifiedAdapter{Adapter: adapter}
	result, err := executeLocalTaskWithAdapter(runCtx, task, classified)
	if err != nil {
		return failure("STORAGE_WRITE_FAILED")
	}
	if result.Status == "succeeded" && result.Metrics.SuccessCount == 0 && result.Metrics.ErrorCount > 0 {
		classified.mu.Lock()
		code := classified.firstError
		classified.mu.Unlock()
		if !isRunnerErrorCode(code) {
			code = ErrConnectionFailed
		}
		result.Status = "failed"
		result.ErrorCode = code
	}
	return result, result.ErrorCode
}

func runnerAPIClient(endpoint string, tlsConfig *tls.Config) (*http.Client, *http.Transport) {
	u, _ := url.Parse(endpoint)
	transport := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second}
	if u.Scheme == "http" {
		transport.DialContext = loopbackAPIDial
	} else {
		transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport
}

func claimLocalCredential(parent context.Context, origin *url.URL, task RunnerTask, tlsConfig *tls.Config) (string, error) {
	endpoint := *origin
	endpoint.Path = "/api/v1/runner-tasks/" + task.RunID + "/credential"
	data, err := json.Marshal(struct {
		Target Target `json:"target"`
	}{task.Config.Target})
	if err != nil {
		return "", errors.New("credential request invalid")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(data))
	if err != nil {
		return "", errors.New("credential request invalid")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-InferLab-Runner-Token", task.TaskToken)
	client, transport := runnerAPIClient(endpoint.String(), tlsConfig)
	defer transport.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("credential request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("credential unavailable")
	}
	var credential struct {
		APIKey string `json:"api_key"`
		Target Target `json:"target"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&credential); err != nil || credential.Target != task.Config.Target || credential.APIKey == "" || len(credential.APIKey) > 4096 || strings.ContainsAny(credential.APIKey, "\r\n") {
		return "", errors.New("credential response invalid")
	}
	return credential.APIKey, nil
}

func submitLocalResult(parent context.Context, endpoint, token string, result RunnerResult, tlsConfig *tls.Config) error {
	data, err := json.Marshal(result)
	if err != nil {
		return errors.New("result encoding failed")
	}
	// A canceled workload still needs a short independent window to persist its terminal state.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid result request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-InferLab-Runner-Token", token)
	client, transport := runnerAPIClient(endpoint, tlsConfig)
	defer transport.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return errors.New("result transport failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return errors.New("result rejected")
	}
	var accepted struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&accepted); err != nil || accepted.RunID != result.RunID || accepted.Status != result.Status {
		return errors.New("result acknowledgment invalid")
	}
	return nil
}

func loopbackAPIDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid API address")
	}
	if strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("HTTP API must remain on loopback")
	}
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
}
