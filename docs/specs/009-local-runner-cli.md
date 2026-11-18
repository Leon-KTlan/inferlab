# 本地 Runner 使用闭环

状态：`accepted`；实现和 staging 验证状态分别记录，规格接受不表示已经上线。

## 目标和范围

让用户从网页创建 OpenAI-compatible 任务，下载任务文件，在自己的电脑运行
CLI，并在原浏览器查看脱敏结果。复用现有 Go Runner、任务协议和报告接口。
本阶段不增加账号、团队、支付、Redis 或云端代发真实请求。

## 使用协议

1. 网页创建实验，再调用 `POST /api/v1/experiments/{id}/runner-tasks`。
2. 返回的任务保存在页面内存，用户主动下载 `task.json`。文件含绑定 Run 的
   `task_token`，应作为临时凭据保护；不能存入 localStorage、URL 或日志。
3. 用户在 `services/api` 目录运行：

   ```bash
   go run . runner --task /path/task.json --api-url http://127.0.0.1:4173 --authorize-target
   ```

4. 网页可在 Endpoint 旁输入 API Key，以独立的任务创建请求发送到同源 API。
   Key 仅在服务端内存暂存5分钟，最多64条；领取、取消或服务关闭时清除，不进入
   配置、任务文件、数据库、浏览器存储或日志。提交后清空输入。
   任务以 `credential_available: true` 标记临时交接，CLI 在授权和目标校验后，
   通过固定 `POST /api/v1/runner-tasks/{run_id}/credential` 路径携任务令牌一次性
   领取。服务端和 CLI 均核对目标快照；不跟随重定向，非本机必须 HTTPS。
   领取失败须在发送目标请求前退出，不能自动重跑；任务文件因此也属于短期取密凭据。
   未填写 Key 时仍支持本地 `OPENAI_API_KEY`。`--authorize-target` 必须显式指定。
5. CLI 验证任务、执行有界请求，并用 `X-InferLab-Runner-Token` 回传终态结果。
   原浏览器继续轮询 Run 并显示报告；等待 CLI 时明确展示等待本地执行。

## 信任边界

- `--api-url` 是用户显式选择的 API origin；生产使用 HTTPS，本机开发仅允许
  回环 HTTP。回传地址必须是匹配任务 Run ID 的固定相对 API 路径。
- 不接受任务指定的绝对回传 URL、跨站回传或重定向；目标 API Key 只发送给
  经过 SSRF 校验的目标，任务 token 只发送给用户选择的 InferLab API。
- 目标继续执行 HTTPS、DNS/IP、重定向、并发、RPS、请求数、阶段时长及请求
  超时限制。测试通过私有依赖注入使用 fake TLS 服务，生产不得提供关闭 TLS
  验证或放开内网的通用开关。
- Ctrl+C 必须取消本地在途请求。网页取消与本地停止不是同一个动作；只有具备
  已验证的 Runner 取消通信时才能宣称网页能立即停止本地请求。
- 结果必须验证计数、阶段与总体一致性，报告按实际来源标注。外部 Runner
  回传结果属于客户端上报，不能宣称经过云端独立测量或防篡改认证。
- 本阶段以请求数、输出限制及时间控制消耗；未有供应商价格和实际 usage
  核算证据时，不承诺精确金额预算或测得 token 吞吐。

## 验收和停止条件

- fake TLS 目标、真实任务 API 与 CLI 形成执行及回传闭环；Mock 路径保持可用。
- 覆盖无授权、恶意回传地址、超时、取消、回传失败和敏感字段不外泄。
- 网页覆盖任务下载、命令交接、等待、失败和完成报告；有最小浏览器验证。
- Go vet/test/race、前端 lint/typecheck/build/test 和现有 CI 通过。
- 未取得具体 Endpoint、模型、授权和预算前，不执行任何真实供应商测试。
- 云端发布仍需真实 staging 凭据、HTTPS 与发布验收证据。

## 错误诊断协议

本地 Runner 终态结果可附 `diagnostics`，每条包含错误分类、实际 HTTP 状态、
白名单上游错误码/参数、网络失败分类、出现次数、首次/末次时间和最近耗时。
最多16种组合，按组合聚合而非保存每次请求日志；旧 Runner 可不附该字段。
服务端验证枚举、次数、时间窗与数值后保存在 Run JSON，无 schema 迁移。
网页与终端显示具体状态、中文解释及排查建议；缺少历史诊断时仅显示分类解释，
不得从旧 `TARGET_HTTP_4XX` 推断具体状态或上游原因。

只解析最多16KiB上游错误 JSON，且仅保留预定义的 `error.code/type/param` 值。
未知值不记录。原始 message、响应正文、请求头、Key、URL 和 Prompt 均不进入
诊断/日志。终端打印任务校验、凭据来源、执行预算、首个失败和最终诊断汇总，
不打印任务令牌、API Key 或任意用户提供的字符串。不新增实时远程日志协议。

验收：400/404/422/429/5xx、参数不兼容、DNS/TLS/连接/超时分类；恶意错误内容
不外泄；聚合在并发下安全；API 拒绝非法诊断；刷新后能读取已回传诊断。

## 发布状态与未决问题

本地实现和自动化验收完成后更新 PR 证据；当前没有真实供应商或 staging
验收证据，因此不标记线上完成。后续独立任务包括：远程取消本地 Runner、
供应商 usage/价格核算，以及经过授权的真实 Endpoint 验证。
