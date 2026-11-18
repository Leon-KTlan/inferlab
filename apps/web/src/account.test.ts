import { describe, expect, it } from 'vitest'
import { validateAccount } from './AccountPanel'

describe('account validation', () => {
  it('uses bcrypt byte limits without truncating unicode passwords', () => {
    expect(validateAccount('Alice_1', 'x'.repeat(72))).toBe('')
    expect(validateAccount('alice', 'x'.repeat(73))).not.toBe('')
    expect(validateAccount('alice', '界'.repeat(24))).toBe('')
    expect(validateAccount('alice', '界'.repeat(25))).not.toBe('')
    expect(validateAccount('alice', 'short')).not.toBe('')
  })
  it('requires a bounded ASCII username', () => {
    expect(validateAccount('中文用户', 'test-password-fixture')).not.toBe('')
    expect(validateAccount('a'.repeat(33), 'test-password-fixture')).not.toBe('')
  })
})
