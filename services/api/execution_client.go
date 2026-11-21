package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

type executionClaim struct {
	Task   RunnerTask `json:"task"`
	APIKey string     `json:"api_key"`
}

// Every API request is fixed to an explicitly trusted origin; capabilities stay in headers/bodies.
func executionRequest(ctx context.Context, origin *url.URL, path, method, token, worker string, input, output any, tlsConfig *tls.Config) (int, error) {
	endpoint := *origin
	endpoint.Path = path
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("X-InferLab-Runner-Token", token)
	}
	if worker != "" {
		request.Header.Set("X-InferLab-Worker-Token", worker)
	}
	client, transport := runnerAPIClient(endpoint.String(), tlsConfig)
	defer transport.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("API transport failed (%s)", networkFailure(err))
	}
	defer response.Body.Close()
	if (response.StatusCode == 200 || response.StatusCode == 202) && output != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(output); err != nil {
			return response.StatusCode, fmt.Errorf("API response invalid")
		}
	}
	return response.StatusCode, nil
}
func runLeaseTask(ctx context.Context, origin *url.URL, claim executionClaim, options runnerCLIOptions, output io.Writer) int {
	task := claim.Task
	validationCtx, vcancel := context.WithTimeout(ctx, 5*time.Second)
	err := validateRunnerTask(validationCtx, task, options.targetPolicy)
	vcancel()
	if err != nil || task.Execution == nil {
		fmt.Fprintln(output, "EXECUTION_TASK_INVALID: 任务校验失败，未发送目标请求。")
		return 1
	}
	key := claim.APIKey
	claim.APIKey = ""
	if key == "" && task.Execution.Mode == "local_agent" {
		key = os.Getenv("OPENAI_API_KEY")
	}
	workload, cancel := context.WithCancel(ctx)
	defer cancel()
	check := func() bool {
		controlCtx, done := context.WithTimeout(workload, 750*time.Millisecond)
		defer done()
		status, err := executionRequest(controlCtx, origin, task.ControlEndpoint, "GET", task.TaskToken, "", nil, nil, options.apiTLSConfig)
		return err == nil && status == 200
	}
	if !check() {
		fmt.Fprintln(output, "EXECUTION_LEASE_LOST: 任务已终止或控制连接不可用，未发送目标请求。")
		return 1
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-workload.Done():
				return
			case <-ticker.C:
				if !check() {
					cancel()
					return
				}
			}
		}
	}()
	result, _ := runValidatedTask(workload, task, key, options.targetPolicy, output)
	key = ""
	cancel()
	<-stopped
	submitCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	status, err := executionRequest(submitCtx, origin, task.ResultEndpoint, "POST", task.TaskToken, "", result, nil, options.apiTLSConfig)
	if err != nil || status != 202 {
		fmt.Fprintln(output, "EXECUTION_RESULT_REJECTED: 结果未接受；任务可能已取消或租约失效。不要自动重跑目标。")
		return 1
	}
	fmt.Fprintf(output, "执行结果已回传：%s，完成 %d，错误 %d。\n", result.Status, result.Metrics.CompletedCount, result.Metrics.ErrorCount)
	if result.Status != "succeeded" {
		return 1
	}
	return 0
}
func runAgentCLI(ctx context.Context, args []string, input io.Reader, output io.Writer, options runnerCLIOptions) int {
	flags := flag.NewFlagSet("agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	apiURL := flags.String("api-url", "", "trusted InferLab origin")
	authorized := flags.Bool("authorize-target", false, "review target authorization")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !*authorized {
		fmt.Fprintln(output, "TARGET_AUTH_REQUIRED: agent --api-url ORIGIN --authorize-target; 私人领取JSON仅从stdin读取。")
		return 2
	}
	origin, err := trustedAPIOrigin(*apiURL)
	if err != nil {
		fmt.Fprintln(output, "RUNNER_API_URL_INVALID")
		return 2
	}
	var payload struct {
		RunID string `json:"run_id"`
		Code  string `json:"code"`
	}
	decoder := json.NewDecoder(io.LimitReader(input, 2048))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || !validRunnerID(payload.RunID, "run_") || !validRunnerID(payload.Code, "claim_") {
		fmt.Fprintln(output, "CLAIM_INPUT_INVALID")
		return 2
	}
	claimCtx, done := context.WithTimeout(ctx, 5*time.Second)
	var claim executionClaim
	status, err := executionRequest(claimCtx, origin, "/api/v1/runner-claims", "POST", "", "", payload, &claim, options.apiTLSConfig)
	done()
	payload.Code = ""
	if err != nil || status != 200 {
		fmt.Fprintln(output, "CLAIM_INVALID: 领取失败、过期或已经使用；请取消原任务后重新提交，不要重跑目标。")
		return 1
	}
	return runLeaseTask(ctx, origin, claim, options, output)
}
func runWorker(ctx context.Context, args []string, output io.Writer, options runnerCLIOptions) int {
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	apiURL := flags.String("api-url", "", "trusted control origin")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		fmt.Fprintln(output, "WORKER_ARGUMENT_INVALID")
		return 2
	}
	origin, err := trustedAPIOrigin(*apiURL)
	token := os.Getenv("PLATFORM_WORKER_TOKEN")
	if err != nil || len(token) < 32 {
		fmt.Fprintln(output, "WORKER_CONFIG_INVALID: require trusted API origin and PLATFORM_WORKER_TOKEN (32+ bytes)")
		return 2
	}
	instance := newID("worker")
	for ctx.Err() == nil {
		var claim executionClaim
		requestCtx, done := context.WithTimeout(ctx, 2*time.Second)
		status, err := executionRequest(requestCtx, origin, "/internal/worker/poll", "POST", "", token, map[string]string{"instance": instance}, &claim, options.apiTLSConfig)
		done()
		if err == nil && status == 200 {
			if claim.Task.Execution == nil || claim.Task.Execution.Mode != "platform" {
				fmt.Fprintln(output, "WORKER_TASK_INVALID")
				return 1
			}
			runLeaseTask(ctx, origin, claim, options, output)
		} else if status == 403 {
			fmt.Fprintln(output, "WORKER_UNAUTHORIZED")
			return 1
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(500 * time.Millisecond):
		}
	}
	return 0
}
