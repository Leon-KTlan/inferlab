const API_BASE = (import.meta.env.VITE_API_BASE_URL ?? '/api/v1').replace(/\/$/, '')

export type RunStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'canceled'

export interface ExperimentInput {
  name: string
  target: { type: 'mock' | 'openai_compatible'; endpoint?: string; model: string }
  load: { concurrency_steps: number[]; stage_seconds: number; max_requests: number; max_rps: number; timeout_ms: number }
  slo: { p95_e2e_ms: number; error_rate_max: number }
}

export interface Run {
  id: string
  status: RunStatus
  experiment_id?: string
  target_type?: 'mock' | 'openai_compatible'
  progress?: number
  sent?: number
  completed?: number
  failed?: number
  current_stage?: number
  total_stages?: number
  current_stage_p95_ms?: number
  updated_at?: string
  error?: string
  events?: Array<{ timestamp?: string; message: string }>
}

interface BackendExperiment {
  experiment_id: string
  name: string
  target: ExperimentInput['target']
  workload: ExperimentInput['load']
  slo: ExperimentInput['slo']
}

interface BackendStage {
  stage: number
  concurrency: number
  request_count: number
  completed_count: number
  success_count: number
  error_count: number
  p50_e2e_ms: number
  p95_e2e_ms: number
  error_rate: number
  throughput_rps: number
  duration_seconds: number
  slo_status: string
}

interface BackendRun {
  run_id: string
  experiment_id: string
  status: RunStatus
  source_type: 'mock' | 'openai_compatible'
  result_kind: string
  request_count: number
  completed_count: number
  success_count: number
  error_count: number
  p50_e2e_ms: number
  p95_e2e_ms: number
  error_rate: number
  throughput_rps: number
  duration_seconds: number
  current_stage: number
  stage_summaries: BackendStage[]
  created_at?: string
  started_at?: string
  finished_at?: string
  error_code?: string
  config?: { workload?: ExperimentInput['load'] }
}

interface BackendReport extends BackendRun {
  slo_status: string
  recommendations: string[]
  methodology: string
  note: string
}

export interface Comparison {
  baseline_run_id: string
  compare_run_id: string
  comparable: boolean
  stages: Array<{
    stage: number
    concurrency: number
    baseline: BackendStage
    compare: BackendStage
    delta: { p95_e2e_ms: number; error_rate: number; throughput_rps: number }
  }>
  overall: {
    baseline: Pick<BackendRun, 'request_count' | 'completed_count' | 'success_count' | 'error_count' | 'p50_e2e_ms' | 'p95_e2e_ms' | 'error_rate' | 'throughput_rps' | 'duration_seconds'>
    compare: Pick<BackendRun, 'request_count' | 'completed_count' | 'success_count' | 'error_count' | 'p50_e2e_ms' | 'p95_e2e_ms' | 'error_rate' | 'throughput_rps' | 'duration_seconds'>
    delta: { p95_e2e_ms: number; error_rate: number; throughput_rps: number }
  }
  notes: string[]
}

export interface Report {
  id?: string
  run_id?: string
  target_type?: 'mock' | 'openai_compatible'
  summary?: Record<string, unknown>
  stages?: Array<Record<string, unknown>>
  conclusion?: string
  recommendations?: string[]
  slo?: { p95_e2e_ms?: number; error_rate_max?: number }
}

export class ApiError extends Error {
  code?: string
  status: number
  constructor(message: string, status: number, code?: string) { super(message); this.name = 'ApiError'; this.status = status; this.code = code }
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  let response: Response
  try { response = await fetch(`${API_BASE}${path}`, { credentials: 'include', ...options, headers: { 'Content-Type': 'application/json', ...options?.headers } }) }
  catch { throw new ApiError('无法连接 API，请确认服务已启动。', 0) }
  const body = await response.json().catch(() => null)
  if (!response.ok) throw new ApiError(body?.error?.message ?? '请求失败，请稍后重试。', response.status, body?.error?.code)
  return body as T
}

function toBackendConfig(input: ExperimentInput) {
  return { name: input.name, target: input.target, workload: input.load, slo: input.slo }
}

function normalizeRun(raw: BackendRun, events: Run['events'] = []): Run {
  const current = raw.stage_summaries.find(stage => stage.stage === raw.current_stage) ?? raw.stage_summaries[raw.stage_summaries.length - 1]
  const total = raw.config?.workload?.concurrency_steps.length ?? Math.max(raw.stage_summaries.length, raw.current_stage)
  const progress = total > 0 ? Math.round((raw.stage_summaries.length / total) * 100) : undefined
  return {
    id: raw.run_id,
    experiment_id: raw.experiment_id,
    status: raw.status,
    target_type: raw.source_type,
    progress: raw.status === 'succeeded' ? 100 : progress,
    sent: raw.request_count,
    completed: raw.completed_count,
    failed: raw.error_count,
    current_stage: raw.current_stage,
    total_stages: total,
    current_stage_p95_ms: current?.p95_e2e_ms,
    updated_at: raw.finished_at ?? raw.started_at ?? raw.created_at,
    error: raw.error_code,
    events,
  }
}

async function getNormalizedRun(id: string): Promise<Run> {
  const raw = await request<BackendRun>(`/runs/${encodeURIComponent(id)}`)
  let events: Run['events'] = []
  try {
    const eventBody = await request<{ events: Array<{ at: string; type: string; stage: number }> }>(`/runs/${encodeURIComponent(id)}/events`)
    events = eventBody.events.map(event => ({ timestamp: event.at, message: `${event.type}${event.stage ? ` · 阶段 ${event.stage}` : ''}` }))
  } catch {
    // Keep the last run snapshot usable when the optional event read fails.
  }
  return normalizeRun(raw, events)
}

export const api = {
  createExperiment: (input: ExperimentInput) => request<BackendExperiment>('/experiments', { method: 'POST', body: JSON.stringify(toBackendConfig(input)) }).then(experiment => ({ id: experiment.experiment_id, ...experiment })),
  startRun: (experimentId: string) => request<BackendRun>(`/experiments/${encodeURIComponent(experimentId)}/runs`, { method: 'POST' }).then(raw => normalizeRun(raw)),
  listRuns: () => request<{ runs: BackendRun[] }>('/runs').then(body => body.runs.map(raw => normalizeRun(raw))),
  getRun: (id: string) => getNormalizedRun(id),
  cancelRun: async (id: string) => { await request(`/runs/${encodeURIComponent(id)}/cancel`, { method: 'POST' }); return getNormalizedRun(id) },
  getReport: (id: string) => request<BackendReport>(`/runs/${encodeURIComponent(id)}/report`).then(raw => ({
    id: raw.run_id,
    run_id: raw.run_id,
    target_type: raw.source_type,
    summary: {
      total_requests: raw.request_count,
      success_rate: raw.completed_count ? (raw.success_count / raw.completed_count) * 100 : 0,
      p95_e2e_ms: raw.p95_e2e_ms,
      throughput: raw.throughput_rps,
    },
    stages: raw.stage_summaries.map(stage => ({ ...stage, error_rate: stage.error_rate * 100, slo_met: stage.slo_status === 'pass' })),
    conclusion: raw.slo_status === 'pass' ? '当前采样阶段满足设定 SLO。请在授权的真实目标上复测后再做上线判断。' : raw.slo_status === 'fail' ? '至少一个阶段超出设定 SLO，需要复测并定位瓶颈。' : '当前样本不足以形成容量结论。',
    recommendations: raw.recommendations,
    slo: undefined,
  } satisfies Report)),
  compareRuns: (compareRunId: string, baselineRunId: string) => request<Comparison>(`/reports/${encodeURIComponent(compareRunId)}/compare?baseline_run_id=${encodeURIComponent(baselineRunId)}`),
}
