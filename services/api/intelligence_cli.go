package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func validateIntelligenceTask(ctx context.Context, task RunnerTask, policy EndpointPolicy) error {
	if !validRunnerID(task.RunID, "run_") || task.TaskID != task.RunID || !validRunnerID(task.ExperimentID, "exp_") || task.SourceType != "openai_compatible" || task.Config.Target.Type != "openai_compatible" {
		return errors.New("invalid task identity")
	}
	token, err := base64.RawURLEncoding.DecodeString(task.TaskToken)
	if err != nil || len(token) != 32 || task.ResultEndpoint != "/api/v1/intelligence/runs/"+task.RunID+"/result" {
		return errors.New("invalid task submission binding")
	}
	settings := task.Config.Intelligence
	if settings == nil || settings.SuiteVersion != intelligenceSuiteVersion || settings.Repetitions < 1 || settings.Repetitions > 3 || settings.MockProfile != "" {
		return errors.New("invalid intelligence suite")
	}
	work := task.Config.Workload
	if len(work.ConcurrencySteps) != 1 || work.ConcurrencySteps[0] != 1 || work.StageSeconds != 30 || work.MaxRequests != len(intelligenceProbes())*settings.Repetitions || work.MaxRPS != 1 || work.TimeoutMS != 10000 {
		return errors.New("invalid intelligence request limits")
	}
	common := task.Config
	common.Intelligence = nil
	return validateLocalConfig(ctx, common, policy)
}

func runIntelligenceCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runIntelligenceCLIWithOptions(ctx, args, stdout, stderr, runnerCLIOptions{})
}

func runIntelligenceCLIWithOptions(ctx context.Context, args []string, stdout, stderr io.Writer, options runnerCLIOptions) int {
	flags := flag.NewFlagSet("intelligence-runner", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	taskPath := flags.String("task", "", "downloaded task JSON")
	apiURL := flags.String("api-url", "", "trusted InferLab origin")
	authorized := flags.Bool("authorize-target", false, "authorize the target")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *taskPath == "" || *apiURL == "" {
		fmt.Fprintln(stderr, "RUNNER_ARGUMENT_INVALID: intelligence-runner --task /path/task.json --api-url https://trusted-api.example --authorize-target")
		return 2
	}
	if !*authorized {
		fmt.Fprintln(stderr, "TARGET_AUTH_REQUIRED: review the task and provide --authorize-target")
		return 2
	}
	origin, err := trustedAPIOrigin(*apiURL)
	if err != nil {
		fmt.Fprintln(stderr, "RUNNER_API_URL_INVALID")
		return 2
	}
	task, err := readRunnerTask(*taskPath)
	if err != nil {
		fmt.Fprintln(stderr, "RUNNER_TASK_INVALID")
		return 2
	}
	checkCtx, cancelCheck := context.WithTimeout(ctx, 10*time.Second)
	err = validateIntelligenceTask(checkCtx, task, options.targetPolicy)
	cancelCheck()
	if err != nil {
		fmt.Fprintln(stderr, "RUNNER_TASK_INVALID")
		return 2
	}
	key := os.Getenv("OPENAI_API_KEY")
	if task.CredentialAvailable {
		key, err = claimLocalCredential(ctx, origin, task, options.apiTLSConfig)
		if err != nil {
			fmt.Fprintln(stderr, "CREDENTIAL_UNAVAILABLE: recreate the task")
			return 1
		}
	}
	executeCtx, cancelExecute := context.WithTimeout(ctx, 120*time.Second)
	result := executeIntelligenceTask(executeCtx, task, key, options.targetPolicy, stdout)
	cancelExecute()
	origin.Path = task.ResultEndpoint
	if err := submitIntelligenceResult(ctx, origin.String(), task.TaskToken, result, options); err != nil {
		fmt.Fprintln(stderr, "RUNNER_RESULT_SUBMISSION_FAILED: results were not accepted; do not repeat target requests automatically")
		return 1
	}
	fmt.Fprintf(stdout, "Intelligence result accepted: %s (%d/%d responses)\n", result.Status, len(result.Outcomes), task.Config.Workload.MaxRequests)
	if ctx.Err() != nil {
		return 130
	}
	if result.Status != "succeeded" {
		return 1
	}
	return 0
}

func executeIntelligenceTask(ctx context.Context, task RunnerTask, key string, policy EndpointPolicy, output io.Writer) IntelligenceRunnerResult {
	started := time.Now().UTC()
	result := IntelligenceRunnerResult{RunID: task.RunID, Status: "succeeded", Outcomes: []IntelligenceOutcome{}, StartedAt: &started}
	adapter, err := newOpenAICompatibleAdapterWithContext(ctx, task.Config.Target, key, policy)
	if err != nil {
		result.Status, result.ErrorCode = "failed", ErrEndpointInvalid
		if strings.TrimSpace(key) == "" {
			result.ErrorCode = ErrAuthFailed
		}
	} else {
		defer adapter.Close()
		next := time.Now()
	requests:
		for repetition := 1; repetition <= task.Config.Intelligence.Repetitions; repetition++ {
			for _, probe := range intelligenceProbes() {
				timer := time.NewTimer(time.Until(next))
				select {
				case <-ctx.Done():
					timer.Stop()
					break requests
				case <-timer.C:
				}
				next = time.Now().Add(time.Second)
				began := time.Now()
				requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				content, code := requestIntelligenceAnswer(requestCtx, adapter, probe)
				cancel()
				outcome := IntelligenceOutcome{ProbeID: probe.ID, Repetition: repetition, LatencyMS: float64(time.Since(began)) / float64(time.Millisecond), ErrorCode: code}
				outcome.Passed = code == "" && gradeIntelligence(probe.ID, content)
				result.Outcomes = append(result.Outcomes, outcome)
				fmt.Fprintf(output, "probe=%s repetition=%d passed=%t error=%s\n", probe.ID, repetition, outcome.Passed, code)
			}
		}
	}
	if ctx.Err() != nil {
		result.Status, result.ErrorCode = "canceled", "RUN_CANCELED"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Status, result.ErrorCode = "failed", "RUN_TIMEOUT"
		}
	}
	finished := time.Now().UTC()
	result.FinishedAt = &finished
	return result
}

func requestIntelligenceAnswer(ctx context.Context, adapter *OpenAICompatibleAdapter, probe IntelligenceProbe) (string, string) {
	if _, err := validateEndpoint(ctx, adapter.endpoint.String(), adapter.policy); err != nil {
		return "", ErrSSRFBlocked
	}
	body, err := json.Marshal(map[string]any{
		"model": adapter.model, "max_tokens": 512, "temperature": 0,
		"messages": []map[string]string{{"role": "user", "content": probe.Prompt}},
	})
	if err != nil {
		return "", ErrResponseInvalid
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, adapter.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", ErrEndpointInvalid
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+adapter.apiKey)
	response, err := adapter.client.Do(request)
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return "", ErrTargetTimeout
		case errors.Is(ctx.Err(), context.Canceled):
			return "", ErrTargetCanceled
		default:
			return "", ErrConnectionFailed
		}
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return "", ErrRedirectBlocked
	case response.StatusCode == 401 || response.StatusCode == 403:
		return "", ErrAuthFailed
	case response.StatusCode == 429:
		return "", ErrRateLimited
	case response.StatusCode >= 500:
		return "", ErrHTTP5xx
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return "", ErrHTTP4xx
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return "", ErrResponseRead
	}
	if len(data) > 64<<10 {
		return "", ErrResponseInvalid
	}
	var answer struct {
		Choices []struct {
			Message struct {
				Content *string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &answer); err != nil || len(answer.Choices) != 1 || answer.Choices[0].Message.Content == nil || answer.Choices[0].FinishReason != "stop" {
		return "", ErrResponseInvalid
	}
	return *answer.Choices[0].Message.Content, ""
}

func submitIntelligenceResult(parent context.Context, endpoint, token string, result IntelligenceRunnerResult, options runnerCLIOptions) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-InferLab-Runner-Token", token)
	client, transport := runnerAPIClient(endpoint, options.apiTLSConfig)
	defer transport.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return errors.New("result transport failed")
	}
	defer response.Body.Close()
	var ack struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if response.StatusCode != http.StatusAccepted || json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&ack) != nil {
		return errors.New("result was rejected")
	}
	if ack.RunID != result.RunID || ack.Status != result.Status {
		return errors.New("result acknowledgment mismatch")
	}
	return nil
}
