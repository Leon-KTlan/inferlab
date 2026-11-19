import { ApiError } from './api'

export function validateAccount(username: string, password: string): string {
  if (!/^[a-z0-9_]{3,32}$/.test(username.trim().toLowerCase())) return '用户名须为 3–32 位英文字母、数字或下划线。'
  const bytes = new TextEncoder().encode(password).length
  if (bytes < 12 || bytes > 72) return '密码须为 12–72 字节，中文字符按 UTF-8 字节计算。'
  return ''
}

export function validateRegistration(username: string, password: string, confirmation: string, invite: string): string {
  const invalid = validateAccount(username, password)
  if (invalid) return invalid
  if (password !== confirmation) return '两次输入的密码不一致，请重新填写。'
  if (!invite) return '请填写邀请码。'
  return ''
}

export function accountErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    switch (error.code) {
      case 'USERNAME_UNAVAILABLE': return '此用户名已被使用，请换一个用户名。'
      case 'INVITE_INVALID': return '邀请码无效或注册已暂停，请确认邀请信息后重试。'
      case 'AUTH_RATE_LIMITED': return '尝试次数过多，请在十分钟后重试。'
      case 'LOGIN_INVALID': return '用户名或密码不正确，请重新填写。'
      case 'INVALID_ACCOUNT': return '请检查用户名格式及密码的 12–72 字节限制。'
      case 'SESSION_CHANGED': return '当前匿名身份已变更，请刷新页面后重试。'
      case 'ALREADY_SIGNED_IN': return '当前已登录，请刷新页面查看账户。'
      case 'CROSS_SITE_REQUEST': return '当前页面来源校验失败，请从本站重新打开后重试。'
    }
    if (error.status === 0) return '无法连接服务，请检查网络后重试。'
  }
  return '账户操作暂未成功，请稍后重试。'
}
