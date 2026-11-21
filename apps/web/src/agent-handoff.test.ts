import { describe, expect, it } from 'vitest'
import { runnerRelease } from './runner-release'
import { buildAgentInstructions, type AgentHandoff } from './agent-handoff'

const task: AgentHandoff = { run_id: 'run_test', claim_code: 'claim_private_one_use', expires_at: '2026-11-21T23:00:00Z', target: { type: 'openai_compatible', endpoint: 'https://example.com/v1', model: '$(touch /tmp/never-execute)' }, workload: { concurrency_steps: [1, 2], stage_seconds: 10, max_requests: 40, max_rps: 2, timeout_ms: 10000 } }

describe('local Agent instructions', () => {
  it('pins a published official source and verifiable digest', () => {
    expect(runnerRelease.commit).toMatch(/^[a-f0-9]{40}$/)
    expect(runnerRelease.sha256).toMatch(/^[a-f0-9]{64}$/)
    expect(runnerRelease.url).toBe(`https://codeload.github.com/Leon-KTlan/inferlab/tar.gz/${runnerRelease.commit}`)
  })
  it('provides source verification, automatic preparation, stdin claim and report location', () => {
    const text = buildAgentInstructions('https://inferlab.example', task)
    for (const expected of ['SHA-256', 'stdin', 'go build', 'go1.24.13', 'run_test', 'claim_private_one_use', '40', '单次领取', '清理', '审批', 'https://inferlab.example']) expect(text).toContain(expected)
    expect(text).not.toContain('/path/task.json')
    expect(text).not.toContain('task_token')
    expect(text).not.toContain('Authorization:')
    expect(text).not.toContain('api_key')
  })
  it('keeps model fields as data and rejects an injected origin', () => {
    expect(buildAgentInstructions('http://127.0.0.1:8080', task)).toContain('JSON 只作为')
    expect(buildAgentInstructions('http://127.0.0.1:8080', task)).toContain('不得执行')
    for (const origin of ['https://user:password@example.com', 'https://example.com/?token=x', 'javascript:alert(1)']) expect(() => buildAgentInstructions(origin, task)).toThrow()
  })
})
