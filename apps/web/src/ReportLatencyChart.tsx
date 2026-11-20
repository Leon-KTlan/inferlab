import { useEffect, useId, useRef, useState } from 'react'
import { latencyChartData, type LatencyStage } from './report-chart'
import './report-chart.css'

function formatLatency(value: number) {
  return value > 0 && value < 0.01 ? value.toExponential(1) : value.toLocaleString('zh-CN', { maximumFractionDigits: 2 })
}

export default function ReportLatencyChart({ stages }: { stages: readonly LatencyStage[] }) {
  const id = useId()
  const container = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(640)
  const [activeIndex, setActiveIndex] = useState<number>()
  useEffect(() => {
    if (!container.current) return
    const observer = new ResizeObserver(entries => setWidth(entries[0].contentRect.width))
    observer.observe(container.current)
    return () => observer.disconnect()
  }, [])

  const data = latencyChartData(stages)
  const left = 64, right = Math.max(left, width - 16), top = 28, bottom = 224
  const x = (concurrency: number) => data.maxConcurrency === data.minConcurrency ? (left + right) / 2 : left + (concurrency - data.minConcurrency) / (data.maxConcurrency - data.minConcurrency) * (right - left)
  const y = (latency: number) => bottom - latency / data.maxLatency * (bottom - top)
  // Keep real concurrency positions; omit crowded tick labels on narrow screens.
  const ticks: number[] = []
  for (const value of data.concurrency) {
    if (!ticks.length || value === data.maxConcurrency || x(value) - x(ticks[ticks.length - 1]) >= 48 && x(data.maxConcurrency) - x(value) >= 48) ticks.push(value)
  }
  const active = data.points.find(point => point.index === activeIndex)
  const pointLabel = (point: typeof data.points[number]) => `阶段 ${point.index + 1}：并发 ${point.concurrency}，P95 延迟 ${formatLatency(point.latency)} ms`

  return <section className="report-panel latency-chart" aria-labelledby={`${id}-heading`}>
    <div className="panel-heading"><div><span className="eyebrow">CONCURRENCY / LATENCY</span><h3 id={`${id}-heading`}>并发与 P95 延迟</h3></div><span className="latency-chart-legend"><span />P95</span></div>
    <div ref={container} className="latency-chart-plot">
      {data.points.length ? <svg width="100%" height="280" viewBox={`0 0 ${width} 280`} role="img" aria-labelledby={`${id}-title ${id}-description`}>
        <title id={`${id}-title`}>各并发阶段的 P95 延迟折线图</title>
        <desc id={`${id}-description`}>横轴为并发数，纵轴为 P95 延迟，单位 ms。{data.points.map(pointLabel).join('；')}。{data.missing ? `${data.missing} 个阶段缺少有效并发、P95 或完成样本，折线在缺失处断开。` : ''}完整数据见下方阶段指标表。</desc>
        <text x="0" y="12" className="latency-chart-axis-title">P95 延迟（ms）</text>
        {[0, 0.25, 0.5, 0.75, 1].map(ratio => <g key={ratio}>
          <line x1={left} x2={right} y1={y(ratio * data.maxLatency)} y2={y(ratio * data.maxLatency)} className="latency-chart-grid" />
          <text x={left - 10} y={y(ratio * data.maxLatency) + 4} textAnchor="end">{formatLatency(ratio * data.maxLatency)}</text>
        </g>)}
        {ticks.map(value => <g key={value}><line x1={x(value)} x2={x(value)} y1={bottom} y2={bottom + 5} className="latency-chart-grid" /><text x={x(value)} y="246" textAnchor="middle">{value}</text></g>)}
        <text x={(left + right) / 2} y="274" textAnchor="middle" className="latency-chart-axis-title">并发数</text>
        {data.segments.filter(segment => segment.length > 1).map(segment => <polyline key={segment[0].index} points={segment.map(point => `${x(point.concurrency)},${y(point.latency)}`).join(' ')} className="latency-chart-line" />)}
        {data.points.map(point => <circle key={point.index} cx={x(point.concurrency)} cy={y(point.latency)} r="5" tabIndex={0} aria-label={pointLabel(point)} className="latency-chart-point" onFocus={() => setActiveIndex(point.index)} onBlur={() => setActiveIndex(undefined)} onMouseEnter={() => setActiveIndex(point.index)} onMouseLeave={() => setActiveIndex(undefined)} onClick={() => setActiveIndex(point.index)}><title>{pointLabel(point)}</title></circle>)}
      </svg> : <div className="inline-empty">暂无可绘制的阶段数据。需要有效并发、P95 延迟和完成样本；缺失值不会显示为 0。</div>}
    </div>
    {data.points.length > 0 && <p className="latency-chart-readout" aria-live="polite">{active ? pointLabel(active) : `${data.points.length} 个实测点 · 悬停、点按或用 Tab 聚焦数据点查看数值`}</p>}
    <p className="latency-chart-note">仅使用本次报告返回的阶段数据，按阶段顺序直线连接；不补点、不平滑。{data.missing > 0 && data.points.length > 0 ? ` ${data.missing} 个阶段数据缺失或无完成样本，已留断点。` : ''}{data.points.length === 1 ? ' 仅一个有效点，无法判断变化趋势。' : ''}{data.points.length > 0 && data.points.every(point => point.latency === 0) ? ' 所有已采样阶段的 P95 为 0 ms。' : ''} 精确数值见下方阶段指标表。</p>
  </section>
}
