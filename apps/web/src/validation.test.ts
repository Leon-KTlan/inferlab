import { describe, expect, it } from 'vitest'
import { validateExperimentForm, ExperimentForm } from './validation'

const mockForm: ExperimentForm = {
  name: 'Mock 体检', target: { type: 'mock', model: 'mock-llm' },
  load: { concurrency_steps: [1, 5], stage_seconds: 20, max_requests: 50, max_rps: 5, timeout_ms: 10000 },
  slo: { p95_e2e_ms: 2000, error_rate_max: 0.01 }, authorization: false,
}

describe('validateExperimentForm', () => {
  it('accepts a valid Mock experiment without authorization', () => expect(validateExperimentForm(mockForm)).toBe(''))
  it('rejects a real target without HTTPS', () => expect(validateExperimentForm({ ...mockForm, target: { type: 'openai_compatible', endpoint: 'http://localhost/v1', model: 'model' }, authorization: true })).toContain('HTTPS'))
  it('requires authorization for a real target', () => expect(validateExperimentForm({ ...mockForm, target: { type: 'openai_compatible', endpoint: 'https://api.example.com/v1', model: 'model' } })).toContain('授权'))
})
