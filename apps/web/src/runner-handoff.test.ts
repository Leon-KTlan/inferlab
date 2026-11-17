import { describe, expect, it } from "vitest";
import {
  buildRunnerCommand,
  RunnerTask,
  serializeRunnerTask,
} from "./api";

const task: RunnerTask = {
  task_id: "run_123",
  run_id: "run_123",
  experiment_id: "exp_123",
  source_type: "openai_compatible",
  config: {
    target: { type: "openai_compatible", endpoint: "https://api.example.com/v1", model: "test-model" },
    workload: { concurrency_steps: [1], stage_seconds: 2, max_requests: 10, max_rps: 2, timeout_ms: 1000 },
    slo: { p95_e2e_ms: 1000, error_rate_max: 0.05 },
  },
  task_token: "capability-only-test-token",
  result_endpoint: "/api/v1/runner-tasks/run_123/result",
};

describe("local Runner handoff", () => {
  it("serializes the complete task for the one-time download", () => {
    const serialized = serializeRunnerTask(task);
    const parsed = JSON.parse(serialized);
    expect(parsed.task_token).toBe(task.task_token);
    expect(parsed.config.target.endpoint).toBe(task.config.target.endpoint);
    expect(parsed.result_endpoint).toBe(task.result_endpoint);
    expect(serialized).not.toContain("OPENAI_API_KEY");
  });

  it("builds the local CLI command with the current same-origin API URL", () => {
    expect(buildRunnerCommand("http://127.0.0.1:4173")).toBe(
      "cd services/api && go run . runner --task /path/task.json --api-url http://127.0.0.1:4173 --authorize-target",
    );
  });
});
