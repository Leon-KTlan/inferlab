import { useState } from 'react'
import { Check, CircleAlert, Copy, Download } from 'lucide-react'
import { buildRunnerCommand, downloadRunnerTask, type RunnerTask } from './api'

// The file protocol remains an explicit advanced option; new Agent tasks never use it.
export default function LegacyRunnerHandoff({ runId, task }: { runId: string; task?: RunnerTask }) {
  const [copied, setCopied] = useState(false)
  const command = buildRunnerCommand(window.location.origin)
  const copy = async () => { try { await navigator.clipboard.writeText(command); setCopied(true); window.setTimeout(() => setCopied(false), 1500) } catch { setCopied(false) } }
  return <section className="events-panel runner-handoff" aria-label="本地 Runner 启动步骤"><div className="panel-heading"><div><span className="eyebrow">ADVANCED LOCAL CLI</span><h3>{task ? '等待本地 Runner' : '任务文件不可恢复'}</h3></div>{task ? <Download size={18} /> : <CircleAlert size={18} />}</div>{task ? <><p className="handoff-copy">Run {runId} 已建立。高级文件协议：任务令牌只保存在当前页面内存，刷新后清除。</p><button className="secondary task-download" onClick={() => downloadRunnerTask(task)}><Download size={16} />下载 task.json</button><p className="handoff-copy">将 task.json 路径填入命令。{task.credential_available ? 'API Key 已临时交接，请在5分钟内启动Runner，无需在终端输入 Key。任务文件含一次性领取凭据，请妥善保管。' : <>未填写Key，请先设置本地 <code>OPENAI_API_KEY</code> 环境变量。</>}</p><div className="command-row"><code>{command}</code><button className="secondary" onClick={copy} aria-label="复制 Runner 命令">{copied ? <Check size={15} /> : <Copy size={15} />}{copied ? '已复制' : '复制命令'}</button></div></> : <p className="handoff-copy">刷新后任务能力令牌已清除，无法重新下载。已启动则等待回传；尚未启动请取消旧任务后重新创建。新版默认入口可一键复制完整指令给Agent。</p>}</section>
}
