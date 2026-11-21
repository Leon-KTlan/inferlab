import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import ReportLatencyChart from './ReportLatencyChart'
import { latencyChartData } from './report-chart'

describe('report latency chart', () => {
  it('uses reported P95 and numeric concurrency without mutating or resampling stages', () => {
    const stages = [
      { concurrency: 1, p95_e2e_ms: 18.42, completed_count: 10 },
      { concurrency: 5, p95_e2e_ms: 79, completed_count: 20 },
      { concurrency: 20, p95_e2e_ms: 204, completed_count: 30 },
    ]
    const before = structuredClone(stages)
    const data = latencyChartData(stages)
    expect(data.points.map(({ concurrency, latency }) => [concurrency, latency])).toEqual([[1, 18.42], [5, 79], [20, 204]])
    expect(data.segments).toHaveLength(1)
    expect(stages).toEqual(before)
    const html = renderToStaticMarkup(<ReportLatencyChart stages={stages} />)
    const positions = [...html.matchAll(/<circle[^>]*cx="([\d.]+)"/g)].map(match => Number(match[1]))
    expect(positions).toHaveLength(3)
    expect((positions[1] - positions[0]) / (positions[2] - positions[0])).toBeCloseTo(4 / 19)
    expect(html).toContain('P95 延迟（ms）')
    expect(html).toContain('并发数')
    expect(html).toContain('阶段 1：并发 1，P95 延迟 18.42 ms')
    expect(html.match(/tabindex="0"/g)).toHaveLength(3)
    expect(html).not.toContain('<path')
  })

  it('breaks lines at missing or invalid data and stages with no completed samples', () => {
    const stages = [
      { concurrency: 1, p95_e2e_ms: 10 },
      { concurrency: 2, p95_e2e_ms: 20 },
      { concurrency: 3, p95_e2e_ms: null },
      { concurrency: 4, p95_e2e_ms: 40 },
      { concurrency: 5, p95_e2e_ms: 0, completed_count: 0 },
      { concurrency: 6, p95_e2e_ms: 60 },
      { concurrency: 7, p95_e2e_ms: 70 },
    ]
    const data = latencyChartData(stages)
    expect(data.points.map(point => point.concurrency)).toEqual([1, 2, 4, 6, 7])
    expect(data.segments.map(segment => segment.map(point => point.concurrency))).toEqual([[1, 2], [4], [6, 7]])
    expect(data.missing).toBe(2)
    const html = renderToStaticMarkup(<ReportLatencyChart stages={stages} />)
    expect(html.match(/<polyline /g)).toHaveLength(2)
    expect(html).toContain('2 个阶段数据缺失或无完成样本，已留断点')
    for (const invalid of [undefined, null, '0', -1, NaN, Infinity]) {
      expect(latencyChartData([{ concurrency: 1, p95_e2e_ms: invalid }]).points).toHaveLength(0)
    }
    for (const invalid of [undefined, null, '1', 0, -1, 1.5, NaN, Infinity]) {
      expect(latencyChartData([{ concurrency: invalid, p95_e2e_ms: 10 }]).points).toHaveLength(0)
    }
  })

  it('renders an empty explanation and a single measured dot without a trend line', () => {
    for (const stages of [[], [{ concurrency: 1 }], [{ concurrency: 1, p95_e2e_ms: 0, completed_count: 0 }]]) {
      const html = renderToStaticMarkup(<ReportLatencyChart stages={stages} />)
      expect(html).toContain('暂无可绘制的阶段数据')
      expect(html).not.toContain('<svg')
    }
    const html = renderToStaticMarkup(<ReportLatencyChart stages={[{ concurrency: 5, p95_e2e_ms: 23 }]} />)
    expect(html.match(/<circle /g)).toHaveLength(1)
    expect(html).not.toContain('<polyline')
    expect(html).toContain('仅一个有效点，无法判断变化趋势')
    expect(html).not.toMatch(/NaN|Infinity/)
  })

  it('preserves measured zero latency and keeps a finite axis for all-zero samples', () => {
    const stages = [{ concurrency: 1, p95_e2e_ms: 0, completed_count: 4 }, { concurrency: 10, p95_e2e_ms: 0, completed_count: 8 }]
    expect(latencyChartData(stages).points.map(point => point.latency)).toEqual([0, 0])
    expect(latencyChartData(stages).maxLatency).toBeGreaterThan(0)
    const html = renderToStaticMarkup(<ReportLatencyChart stages={stages} />)
    expect(html.match(/<circle /g)).toHaveLength(2)
    expect(html).toContain('所有已采样阶段的 P95 为 0 ms')
    expect(html).not.toMatch(/NaN|Infinity/)
  })
})
