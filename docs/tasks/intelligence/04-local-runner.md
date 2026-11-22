# 真实目标本地智力检测 Runner

状态：CLI 与本机 TLS fixture 验收通过；完整 staging 验收见主任务。

## 使用

在网页创建真实 OpenAI-compatible 智力检测，确认目标、模型、重复次数和授权后，下载返回的 `task` 对象为独立 JSON 文件。任务文件包含一次性结果回传凭证，应仅保存在本机，建议权限 `0600`；它不包含目标 API Key。`--api-url` 填自己信任的 InferLab API 原点，不包含路径、查询参数或用户信息。

在 `services/api` 目录运行：

```bash
go run . intelligence-runner --task /absolute/path/task.json --api-url https://your-inferlab.example --authorize-target
```

API Key 有两种来源：在启动终端安全设置 `OPENAI_API_KEY` 环境变量；或创建任务时提供临时凭据，由 CLI 使用任务令牌从可信 API 一次性领取。临时凭据仅在服务端内存保留 5 分钟，成功领取即删除，优先于环境变量；过期或已领取应重新创建任务。不要将真实 Key 写入任务 JSON、命令参数、项目文件或日志。

套件固定为 canary-v1；每题重复 1–3 次（默认 2），共 10–30 个请求。运行使用单并发、最多 1 req/s、单请求 10 秒超时、总时限 120 秒、`temperature=0`、`max_tokens=512`。CLI 不执行回答生成的代码。目标必须通过 HTTPS 和 SSRF 检查，拒绝回环、内网、云元数据及重定向；测试的 loopback/TLS 选项仅用于 fixture，没有生产 CLI 绕过参数。

终端只显示题号、轮次、通过状态和白名单错误码，不输出 Prompt、回答正文、API Key 或任务令牌。回答在进程内逐题评分，结果只回传题号、轮次、是否通过、耗时和错误码；API 重算总分。回传 URL 必须精确绑定当前 Run，并从可信 `--api-url` 拼接；不跟随重定向，确认 HTTP 202 和匹配的 Run ID/状态后才打印 accepted。

Ctrl+C 停止目标请求，并尝试用独立的短超时回传取消结果。网页取消仅关闭记录，无法停止已经在终端运行的请求，仍须在终端 Ctrl+C。回传失败时先检查网页记录和网络，不要自动重跑目标请求。任务 15 分钟到期；需要重测时从历史终态 Run 选择复测，按原快照创建新任务，再明确授权并提供或领取新的 API Key。

## 验证与回滚

在 `services/api` 执行：

```bash
gofmt -w intelligence_cli_test.go
go test -run '^TestIntelligenceCLI' ./...
```

TLS fixture 仅在本机运行一轮十题（约 9 秒），验证真实 HTTPS 请求数、串行/速率限制、温度与 token 限额、一次性领取凭据、逐题评分及持久化回传；故意包含格式错误和 503，确认答案错误与请求错误被分别记录。额外覆盖无授权、提前取消、SSRF、任务绑定、限额篡改、错误回执及回传重定向；检查终端和保存的 Run 不包含凭据、Prompt 或原始回答。

本任务只新增测试和文档；无需配置或数据迁移，回滚可撤回这两个文件。CLI 本身的集成和回滚由主任务处理。

执行结果：上述格式化和最小测试均通过（`ok inferlab/services/api 9.383s`）。该证据来自本机受控 TLS 服务，没有请求真实供应商。
