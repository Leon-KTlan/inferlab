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

4. CLI 从本地 `OPENAI_API_KEY` 环境变量读取目标 Key。网页、任务文件和结果
   协议不接收 Key。`--authorize-target` 必须显式指定，缺少时不得发送目标请求。
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

## 发布状态与未决问题

本地实现和自动化验收完成后更新 PR 证据；当前没有真实供应商或 staging
验收证据，因此不标记线上完成。后续独立任务包括：远程取消本地 Runner、
供应商 usage/价格核算，以及经过授权的真实 Endpoint 验证。
