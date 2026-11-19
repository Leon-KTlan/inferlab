import { describe, expect, it } from 'vitest'
import { ApiError } from './api'
import { accountErrorMessage, validateRegistration } from './registration'

describe('invitation registration', () => {
  it('requires an exact password confirmation and invitation', () => {
    expect(validateRegistration('alice', 'test-password-fixture', 'different-password', 'fixture')).toBe('两次输入的密码不一致，请重新填写。')
    expect(validateRegistration('alice', 'test-password-fixture', 'test-password-fixture', '')).toBe('请填写邀请码。')
    expect(validateRegistration('alice', 'test-password-fixture', 'test-password-fixture', 'fixture')).toBe('')
  })
  it('measures UTF-8 bytes and rejects oversized passwords without truncation', () => {
    expect(validateRegistration('alice', '界'.repeat(24), '界'.repeat(24), 'fixture')).toBe('')
    expect(validateRegistration('alice', '界'.repeat(25), '界'.repeat(25), 'fixture')).toContain('12–72 字节')
    expect(validateRegistration('alice', 'x'.repeat(73), 'x'.repeat(73), 'fixture')).toContain('12–72 字节')
  })
  it('maps actionable errors without echoing server or credential content', () => {
    expect(accountErrorMessage(new ApiError('secret-sentinel', 409, 'USERNAME_UNAVAILABLE'))).toBe('此用户名已被使用，请换一个用户名。')
    expect(accountErrorMessage(new ApiError('secret-sentinel', 403, 'INVITE_INVALID'))).toBe('邀请码无效或注册已暂停，请确认邀请信息后重试。')
    expect(accountErrorMessage(new ApiError('secret-sentinel', 429, 'AUTH_RATE_LIMITED'))).toContain('十分钟')
    expect(accountErrorMessage(new ApiError('secret-sentinel', 0))).toContain('无法连接')
    expect(accountErrorMessage(new Error('secret-sentinel'))).not.toContain('secret-sentinel')
  })
})
