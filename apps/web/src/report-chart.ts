export interface LatencyStage {
  concurrency?: unknown
  p95_e2e_ms?: unknown
  completed_count?: unknown
}

function nonNegativeNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

export function latencyChartData(stages: readonly LatencyStage[]) {
  const rows = stages.map(stage => ({
    concurrency: nonNegativeNumber(stage.concurrency) && Number.isInteger(stage.concurrency) && stage.concurrency > 0 ? stage.concurrency : undefined,
    latency: nonNegativeNumber(stage.p95_e2e_ms) && stage.completed_count !== 0 ? stage.p95_e2e_ms : undefined,
  }))
  const points: Array<{ concurrency: number; latency: number; index: number }> = []
  const segments: typeof points[] = []
  let segment: typeof points = []
  rows.forEach((row, index) => {
    if (row.concurrency === undefined || row.latency === undefined) {
      if (segment.length) segments.push(segment)
      segment = []
      return
    }
    const point = { concurrency: row.concurrency, latency: row.latency, index }
    points.push(point)
    segment.push(point)
  })
  if (segment.length) segments.push(segment)
  const concurrency = rows.flatMap(row => row.concurrency === undefined ? [] : [row.concurrency])
  const maximum = Math.max(0, ...points.map(point => point.latency))
  const magnitude = 10 ** Math.floor(Math.log10(maximum || 1))
  return {
    points,
    segments,
    concurrency: [...new Set(concurrency)],
    minConcurrency: Math.min(...concurrency),
    maxConcurrency: Math.max(...concurrency),
    maxLatency: maximum ? Math.ceil(maximum / magnitude) * magnitude : 1,
    missing: stages.length - points.length,
  }
}
