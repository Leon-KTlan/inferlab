import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, type ExperimentInput } from './api'

const input: ExperimentInput = {
  name: '固定延迟',
  target: { type: 'mock', model: 'mock-llm', mock_scenario: 'slow-v1' },
  load: { concurrency_steps: [1, 4], stage_seconds: 15, max_requests: 150, max_rps: 5, timeout_ms: 1000 },
  slo: { p95_e2e_ms: 250, error_rate_max: 0.05 },
}
const stage = { stage: 1, concurrency: 1, completed_count: 12, slo_status: 'insufficient_data' }
const run = {
  run_id: 'run_new',
  experiment_id: 'exp_1',
  source_type: 'mock',
  software_version: '0.1.1',
  config: { target: input.target, workload: input.load, slo: input.slo },
  stage_summaries: [stage],
}

afterEach(() => vi.unstubAllGlobals())

describe('可复跑 Mock 实验', () => {
  it('把所选场景随建立实验请求提交', async () => {
    const fetch = vi.fn().mockResolvedValue(Response.json({ experiment_id: 'exp_1' }))
    vi.stubGlobal('fetch', fetch)
    await api.createExperiment(input)
    const [url, options] = fetch.mock.calls[0]
    expect(url).toMatch(/\/experiments$/)
    expect(options.method).toBe('POST')
    expect(JSON.parse(options.body)).toEqual({
      name: input.name, target: input.target, workload: input.load, slo: input.slo,
    })
  })

  it('从旧记录复跑且读取新 Run 的报告快照和样本状态', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(Response.json({ ...run, status: 'queued' }, { status: 202 }))
      .mockResolvedValueOnce(Response.json({ ...run, status: 'succeeded', slo_status: 'insufficient_data' }))
    vi.stubGlobal('fetch', fetch)
    await expect(api.rerunMock('run_source')).resolves.toMatchObject({
      id: 'run_new', status: 'queued', mock_scenario: 'slow-v1', total_stages: 2,
    })
    expect(fetch.mock.calls[0][0]).toMatch(/\/runs\/run_source\/rerun$/)
    expect(fetch.mock.calls[0][1]).toMatchObject({ method: 'POST' })
    await expect(api.getReport('run_new')).resolves.toMatchObject({
      workload: input.load,
      slo: input.slo, mock_scenario: 'slow-v1', software_version: '0.1.1', stages: [stage],
    })
  })
})
