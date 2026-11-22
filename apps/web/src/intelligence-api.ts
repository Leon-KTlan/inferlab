import { ApiError, type RunStatus } from './api'

const BASE = `${(import.meta.env.VITE_API_BASE_URL ?? '/api/v1').replace(/\/$/, '')}/intelligence`
export interface IntelligenceSettings {
  suite_version: string
  repetitions: number
  mock_profile?: 'reference' | 'degraded'
}
export interface IntelligenceProbe {
  id: string
  dimension: string
  title: string
  prompt: string
  expected: string
}
export interface IntelligenceDimension {
  id: string
  label: string
  score: number
  passed: number
  total: number
  completed: number
}
export interface IntelligenceOutcome {
  probe_id: string
  repetition: number
  passed: boolean
  latency_ms: number
  error_code?: string
}
export interface IntelligenceReport {
  suite_version: string
  score: number
  passed: number
  total: number
  completed: number
  error_count: number
  dimensions: IntelligenceDimension[]
  outcomes: IntelligenceOutcome[] | null
  complete: boolean
  conclusion: string
}
export interface IntelligenceConfig {
  name: string
  target: {
    type: 'mock' | 'openai_compatible'
    model: string
    endpoint?: string
  }
  intelligence: IntelligenceSettings
}
export interface IntelligenceRun {
  run_id: string
  status: RunStatus
  source_type: 'mock' | 'openai_compatible'
  config: IntelligenceConfig
  created_at: string
  started_at?: string
  finished_at?: string
  completed_count: number
  duration_seconds: number
  error_code?: string
  intelligence_report?: IntelligenceReport
}
export interface IntelligenceTask {
  task_id: string
  run_id: string
  experiment_id: string
  source_type: string
  config: IntelligenceConfig
  task_token: string
  result_endpoint: string
  credential_available?: boolean
}
export interface IntelligenceInput {
  name: string
  target: IntelligenceConfig['target']
  mock_profile?: 'reference' | 'degraded'
  repetitions: number
  authorized?: boolean
  api_key?: string
}
export interface IntelligenceSuite {
  suite_version: string
  probes: IntelligenceProbe[]
  dimensions: Array<{ id: string; label: string }>
}
export interface RadarGroup {
  id: string
  model: string
  source_type: string
  endpoint_label: string
  suite_version: string
  repetitions: number
  runs: IntelligenceRun[]
  latest_run_id: string
  baseline_run_id?: string
  baseline_score?: number
  delta?: number
  signal: 'review' | 'stable' | 'insufficient_data'
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  let response: Response
  try {
    response = await fetch(`${BASE}${path}`, {
      credentials: 'include',
      cache: 'no-store',
      ...options,
      headers: { 'Content-Type': 'application/json', ...options?.headers },
    })
  } catch {
    throw new ApiError('无法连接检测 API，请检查服务后重试。', 0)
  }
  const body = await response.json().catch(() => null)
  if (!response.ok) {
    const quota = body?.error?.quota
    const message = quota
      ? `请求额度不足，剩余 ${quota.remaining}，${new Date(quota.reset_at).toLocaleString('zh-CN')} 重置。`
      : body?.error?.message
    throw new ApiError(
      message ?? '检测请求失败，请重试。',
      response.status,
      body?.error?.code,
    )
  }
  return body as T
}
const post = (body: unknown = {}) => ({
  method: 'POST',
  body: JSON.stringify(body),
})
export const intelligenceApi = {
  suite: () => request<IntelligenceSuite>('/suite'),
  runs: async () =>
    (await request<{ runs: IntelligenceRun[] }>('/runs')).runs ?? [],
  run: (id: string) =>
    request<IntelligenceRun>(`/runs/${encodeURIComponent(id)}`),
  create: (input: IntelligenceInput) =>
    request<{ run: IntelligenceRun; task?: IntelligenceTask }>(
      '/runs',
      post(input),
    ),
  cancel: async (id: string) => {
    await request<{
      run_id: string
      status: string
      cancel_requested: boolean
    }>(`/runs/${encodeURIComponent(id)}/cancel`, post())
    return request<IntelligenceRun>(`/runs/${encodeURIComponent(id)}`)
  },
  rerun: (
    id: string,
    authorization?: { authorized: boolean; api_key?: string },
  ) =>
    request<{ run: IntelligenceRun; task?: IntelligenceTask }>(
      `/runs/${encodeURIComponent(id)}/rerun`,
      post(authorization),
    ),
  radar: async () =>
    (await request<{ groups: RadarGroup[] }>('/radar')).groups ?? [],
}
export function validateIntelligence(input: IntelligenceInput): string {
  if (!input.name.trim() || !input.target.model.trim())
    return '请填写检测名称与模型。'
  if (new TextEncoder().encode(input.name.trim()).length > 120)
    return '检测名称不能超过 120 字节。'
  if (
    !Number.isInteger(input.repetitions) ||
    input.repetitions < 1 ||
    input.repetitions > 3
  )
    return '每题重复次数必须为 1–3。'
  if (input.target.type === 'openai_compatible') {
    try {
      const url = new URL(input.target.endpoint ?? '')
      if (url.protocol !== 'https:' || url.username || url.password)
        return '请填写不包含凭据的 HTTPS Endpoint。'
    } catch {
      return '请填写有效的 HTTPS Endpoint。'
    }
    if (!input.authorized) return '请确认你拥有目标服务的测试授权。'
  }
  return ''
}
export function intelligenceCommand(origin: string): string {
  return `cd services/api && go run . intelligence-runner --task /path/task.json --api-url ${origin} --authorize-target`
}
export function serializeIntelligenceTask(task: IntelligenceTask): string {
  // Explicit fields keep transient credentials out of the downloaded file.
  return `${JSON.stringify({ task_id: task.task_id, run_id: task.run_id, experiment_id: task.experiment_id, source_type: task.source_type, config: task.config, task_token: task.task_token, result_endpoint: task.result_endpoint, credential_available: task.credential_available }, null, 2)}\n`
}
export function downloadIntelligenceTask(task: IntelligenceTask): void {
  const url = URL.createObjectURL(
    new Blob([serializeIntelligenceTask(task)], { type: 'application/json' }),
  )
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = 'task.json'
  anchor.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}
export const isIntelligenceActive = (run: IntelligenceRun) =>
  run.status === 'queued' || run.status === 'running'
export const isComparableIntelligence = (run: IntelligenceRun) =>
  run.status === 'succeeded' &&
  !run.error_code &&
  !!run.intelligence_report?.complete &&
  run.intelligence_report.error_count === 0
export const intelligenceStatus = (status: RunStatus) =>
  ({
    queued: '等待执行',
    running: '检测中',
    succeeded: '已完成',
    failed: '失败',
    canceled: '已取消',
  })[status]
export const intelligenceSource = (source: string) =>
  source === 'mock' ? 'Mock 模拟' : '真实 API · 本地执行'
export function intelligenceRoute(
  hash: string,
): { view: 'intelligence' | 'radar'; runId?: string } | undefined {
  const [page, query] = hash.replace(/^#/, '').split('?')
  return page === 'intelligence' || page === 'radar'
    ? { view: page, runId: new URLSearchParams(query).get('run') ?? undefined }
    : undefined
}
