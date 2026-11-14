import type { ExperimentInput } from './api'

export type ExperimentForm = ExperimentInput & { authorization: boolean }

export function validateExperimentForm(form: ExperimentForm): string {
  if (!form.name.trim() || !form.target.model.trim()) return '请填写实验名称和模型。'
  if (form.target.type === 'openai_compatible' && !/^https:\/\//.test(form.target.endpoint ?? '')) return '真实目标必须使用 HTTPS 地址。'
  if (form.load.max_requests < 1 || form.load.stage_seconds < 1 || form.load.max_rps <= 0) return '负载预算必须是正数。'
  if (form.target.type === 'openai_compatible' && !form.authorization) return '请确认你拥有目标服务的测试授权。'
  return ''
}
