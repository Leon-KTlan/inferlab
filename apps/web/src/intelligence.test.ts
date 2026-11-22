import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  intelligenceApi,
  intelligenceCommand,
  intelligenceRoute,
  isComparableIntelligence,
  serializeIntelligenceTask,
  validateIntelligence,
  type IntelligenceInput,
  type IntelligenceRun,
  type IntelligenceTask,
} from './intelligence-api'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { RadarChart } from './RadarPage'

const input: IntelligenceInput = {
  name: '能力检测',
  target: { type: 'mock', model: 'mock-llm' },
  mock_profile: 'reference',
  repetitions: 2,
}
afterEach(() => vi.unstubAllGlobals())
describe('智力检测客户端边界', () => {
  it('真实目标必须为无凭据的 HTTPS，并重新授权', () => {
    const real: IntelligenceInput = {
      ...input,
      target: {
        type: 'openai_compatible',
        model: 'test-model',
        endpoint: 'https://example.com/v1',
      },
    }
    expect(validateIntelligence(real)).toContain('授权')
    expect(validateIntelligence({ ...real, authorized: true })).toBe('')
    expect(
      validateIntelligence({
        ...real,
        authorized: true,
        target: { ...real.target, endpoint: 'http://example.com/v1' },
      }),
    ).toContain('HTTPS')
    expect(
      validateIntelligence({
        ...real,
        target: {
          ...real.target,
          endpoint: 'https://key:secret@example.com/v1',
        },
      }),
    ).toContain('凭据')
    expect(validateIntelligence({ ...input, repetitions: 4 })).toContain('1–3')
    expect(validateIntelligence({ ...input, repetitions: 1.5 })).toContain(
      '1–3',
    )
    expect(validateIntelligence({ ...input, name: ' ' })).toContain('名称')
    expect(validateIntelligence(input)).toBe('')
  })
  it('完整保留独立路由、编码后的运行 ID 与雷达入口', () => {
    expect(intelligenceRoute('#intelligence?run=run_%2F1')).toEqual({
      view: 'intelligence',
      runId: 'run_/1',
    })
    expect(intelligenceRoute('#radar')).toEqual({
      view: 'radar',
      runId: undefined,
    })
    expect(intelligenceRoute('#report')).toBeUndefined()
  })
  it('创建和真实复测使用各自的授权请求，不复用密钥', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        Response.json({ run: { run_id: 'run_new' } }, { status: 202 }),
      )
    vi.stubGlobal('fetch', fetch)
    await intelligenceApi.create(input)
    expect(fetch.mock.calls[0][0]).toMatch(/\/intelligence\/runs$/)
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual(input)
    await intelligenceApi.rerun('run_original', { authorized: true })
    expect(fetch.mock.calls[1][0]).toMatch(
      /\/intelligence\/runs\/run_original\/rerun$/,
    )
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({
      authorized: true,
    })
    await intelligenceApi.rerun('run_mock')
    expect(JSON.parse(fetch.mock.calls[2][1].body)).toEqual({})
  })
  it('读取原始 Run、题库和雷达分组', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(Response.json({ runs: [{ run_id: 'run_1' }] }))
      .mockResolvedValueOnce(
        Response.json({ suite_version: 'canary-v1', probes: [] }),
      )
      .mockResolvedValueOnce(Response.json({ groups: [{ id: 'group_1' }] }))
    vi.stubGlobal('fetch', fetch)
    await expect(intelligenceApi.runs()).resolves.toEqual([{ run_id: 'run_1' }])
    await expect(intelligenceApi.suite()).resolves.toMatchObject({
      suite_version: 'canary-v1',
    })
    await expect(intelligenceApi.radar()).resolves.toEqual([{ id: 'group_1' }])
  })
  it('取消接口只有三字段回执时，重新读取完整 Run 才更新详情', async () => {
    const run: IntelligenceRun = {
      run_id: 'run_1',
      status: 'canceled',
      source_type: 'mock',
      config: {
        name: input.name,
        target: input.target,
        intelligence: {
          suite_version: 'canary-v1',
          repetitions: 2,
          mock_profile: 'reference',
        },
      },
      created_at: '2026-11-22T00:00:00Z',
      completed_count: 0,
      duration_seconds: 0,
    }
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        Response.json({
          run_id: 'run_1',
          status: 'canceled',
          cancel_requested: true,
        }),
      )
      .mockResolvedValueOnce(Response.json(run))
    vi.stubGlobal('fetch', fetch)
    await expect(intelligenceApi.cancel('run_1')).resolves.toEqual(run)
    expect(fetch.mock.calls[0][0]).toMatch(
      /\/intelligence\/runs\/run_1\/cancel$/,
    )
    expect(fetch.mock.calls[0][1].method).toBe('POST')
    expect(fetch.mock.calls[1][0]).toMatch(/\/intelligence\/runs\/run_1$/)
    expect(fetch.mock.calls[1][1].method).toBeUndefined()
  })
  it('传输失败返回连接错误，不能成为模型退化信号', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockRejectedValue(new TypeError('network error')),
    )
    await expect(intelligenceApi.radar()).rejects.toThrow('无法连接检测 API')
  })
  it('下载只包含任务字段，CLI 使用当前 origin 与专用子命令', () => {
    const task: IntelligenceTask = {
      task_id: 'task_1',
      run_id: 'run_1',
      experiment_id: 'exp_1',
      source_type: 'openai_compatible',
      config: {
        name: input.name,
        target: {
          type: 'openai_compatible',
          model: 'test-model',
          endpoint: 'https://example.com/v1',
        },
        intelligence: { suite_version: 'canary-v1', repetitions: 2 },
      },
      task_token: 'test-task-capability',
      result_endpoint: '/api/v1/intelligence/runs/run_1/result',
      credential_available: true,
    }
    const serialized = serializeIntelligenceTask({
      ...task,
      api_key: 'should-not-be-in-file',
    } as IntelligenceTask)
    expect(JSON.parse(serialized)).toEqual(task)
    expect(serialized).not.toContain('should-not-be-in-file')
    expect(intelligenceCommand('http://127.0.0.1:4173')).toBe(
      'cd services/api && go run . intelligence-runner --task /path/task.json --api-url http://127.0.0.1:4173 --authorize-target',
    )
  })
  it('只有完整、无运行与传输错误的成功结果可比较', () => {
    const report = {
      suite_version: 'canary-v1',
      score: 100,
      passed: 20,
      total: 20,
      completed: 20,
      error_count: 0,
      dimensions: [],
      outcomes: null,
      complete: true,
      conclusion: '',
    }
    const run: IntelligenceRun = {
      run_id: 'run_1',
      status: 'succeeded',
      source_type: 'mock',
      config: {
        name: input.name,
        target: input.target,
        intelligence: { suite_version: 'canary-v1', repetitions: 2 },
      },
      created_at: '2026-11-22T00:00:00Z',
      completed_count: 20,
      duration_seconds: 1,
      intelligence_report: report,
    }
    expect(isComparableIntelligence(run)).toBe(true)
    expect(isComparableIntelligence({ ...run, status: 'canceled' })).toBe(false)
    expect(isComparableIntelligence({ ...run, status: 'running' })).toBe(false)
    expect(
      isComparableIntelligence({ ...run, error_code: 'TARGET_TIMEOUT' }),
    ).toBe(false)
    expect(
      isComparableIntelligence({
        ...run,
        intelligence_report: { ...report, complete: false },
      }),
    ).toBe(false)
    expect(
      isComparableIntelligence({
        ...run,
        intelligence_report: { ...report, error_count: 1 },
      }),
    ).toBe(false)
  })
  it('五轴图有对应的可访问数值表与基线标记', () => {
    const dimensions = [
      'reasoning',
      'math',
      'instruction',
      'structured',
      'context',
    ].map((id) => ({
      id,
      label: id,
      score: 75,
      passed: 3,
      total: 4,
      completed: 4,
    }))
    const html = renderToStaticMarkup(
      createElement(RadarChart, {
        dimensions,
        baseline: dimensions.map((item) => ({ ...item, score: 90 })),
      }),
    )
    expect(html).toContain('五维能力雷达图')
    expect(html).toContain('<table>')
    expect(html).toContain('历史三次均值')
    expect(html.match(/75\.0/g)).toHaveLength(5)
    expect(html.match(/90\.0/g)).toHaveLength(5)
  })
})
