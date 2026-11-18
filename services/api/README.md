# InferLab API

Mock 由 API 进程内的本地 HTTP 模拟服务执行。OpenAI-compatible 目标通过无凭证的
本地 Runner 任务协议执行。网页可临时交接 API Key，API 进程仅在内存保存5分钟，
供对应 Runner 领取一次，不写入数据库、任务文件或日志。

```bash
go run .
```

默认监听 `127.0.0.1:8080`。`GET /healthz` 检查数据库连通性。

配置环境变量：`LISTEN_ADDR`（监听地址）、`DB_PATH`（默认 `data/inferlab.db`）、`WEB_DIR`（可选，前端构建目录）、`APP_ENV`（local/staging/production）、`SESSION_SECRET`（签名密钥）。staging 和 production 必须提供至少32字节的 `SESSION_SECRET` 并通过 HTTPS 反向代理访问；local 未配置时在数据库保存随机签名密钥，以保持重启后的匿名会话。

## API

浏览器采用 HttpOnly、SameSite=Strict 签名 cookie 隔离匿名访问者，API 为同源访问，不开放 CORS。丢失 cookie 将无法读取原实验；当前没有账号、会话找回或共享报告。

- `POST /api/v1/experiments` 创建配置（见下例）。
- `GET /api/v1/experiments` 返回当前访问者最近100个实验：`{experiments:[...]}`。
- `GET /api/v1/experiments/{id}` 读取配置。
- `POST /api/v1/experiments/{id}/runs` 启动实验，202 返回 Run。
- `POST /api/v1/experiments/{id}/runner-tasks` 创建任务，可选 JSON `api_key` 仅暂存内存；返回的 `RunnerTask` 不含 Key，`task_token` 绑定本次领取和结果回传。
- `POST /api/v1/runner-tasks/{id}/credential` 携任务令牌和目标快照，一次领取临时 Key；领取、取消、5分钟超时或服务关闭后清除。
- `POST /api/v1/runner-tasks/{id}/result` 接收本地 Runner 的终态指标和白名单错误码。
- `GET /api/v1/runs` 返回当前访问者最近100次 Run，用于刷新后恢复工作台。
- `GET /api/v1/runs/{id}` 读取持久化状态、配置快照和实时指标。
- `POST /api/v1/runs/{id}/cancel` 异步幂等取消，继续轮询确认终态。
- `GET /api/v1/runs/{id}/events?after=0` 按事件ID查询，单次最多200条。
- `GET /api/v1/runs/{id}/report` 终态报告，运行中返回409。
- `GET /api/v1/reports/{candidate_run_id}/compare?baseline_run_id={baseline_run_id}` 比较同一访问者的两次 Mock Run，差值为候选减基线。

```json
{
  "name": "Mock release check",
  "target": {"type": "mock", "model": "mock-llm"},
  "workload": {"concurrency_steps": [1, 3, 6], "stage_seconds": 2, "max_requests": 120, "max_rps": 20, "timeout_ms": 1000},
  "slo": {"p95_e2e_ms": 200, "error_rate_max": 0.05}
}
```

并发严格递增且最大32，最多8阶段；每阶段1至30秒、总阶段时间最多120秒；全局请求预算1至1000、RPS为1至50、请求超时10至10000ms。同时最多4个 Run，每个访问者最多1个活跃 Run。请求预算可能在较早阶段耗尽，报告会标明未覆盖阶段。

阶段截止会取消尚在执行的请求并计入错误；用户取消也会保留已发请求的结果。Mock 每17个请求返回一次503，延迟随实际并发变化；低超时可产生超时样本。Mock不产生真实模型费用，不接受 endpoint、密钥、Prompt、Authorization 等输入字段，也不连接用户提供的外部地址。

OpenAI-compatible endpoint 只能是 HTTPS；API 进程拒绝真实目标的直接运行，需本地 Runner 使用自己的环境变量读取 Key。Runner 会发送 `POST <endpoint>/chat/completions`，并执行并发、RPS、总请求、阶段时长和单请求超时限制。任务响应只包含配置快照、Run ID 和结果回传地址：

```text
POST /api/v1/experiments/{id}/runner-tasks
  -> RunnerTask {run_id, config, task_token, result_endpoint}  # 无 API Key
本地进程读取 OPENAI_API_KEY（不写入任务或日志）并执行任务
POST /api/v1/runner-tasks/{run_id}/result
  X-InferLab-Runner-Token: <task_token>
  -> RunnerResult {status, metrics, stage_summaries, error_code}
```

错误码只记录 `TARGET_*` 分类、`RUN_CANCELED` 或存储错误，不包含 URL 凭证、授权头、Prompt 或响应正文。

## 本地 Runner CLI

先在网页创建 OpenAI-compatible 实验并下载 `task.json`，核对其中的目标、模型和请求预算，然后在本机运行：

```bash
cd services/api
go run . runner --task /path/task.json --api-url http://127.0.0.1:8080 --authorize-target
```

可在网页 Endpoint 旁填写 API Key，创建任务后5分钟内执行上述命令，CLI 会凭
任务令牌从可信 API 一次性领取，无需设置环境变量。Key 过期、已领取或服务重启
后需创建新任务。留空时继续从 `OPENAI_API_KEY` 读取；Key 不进入任务、参数、报告。
`--authorize-target` 必须显式提供；缺失时不会领取 Key 或发送目标请求。
任务文件包含领取和回传凭据，按敏感文件保管，使用后删除。

`--api-url` 必须由操作者指定为可信 API 的 origin，不允许凭据、路径、query 或 fragment。生产使用 HTTPS 并验证证书；HTTP 仅允许数字回环地址或 `localhost`。回传路径必须精确匹配 `/api/v1/runner-tasks/{run_id}/result`，CLI 校验 task_id 与 run_id 一致，拒绝任意绝对回传地址，并且目标请求、结果回传均禁止重定向。真实目标继续拒绝回环、私网和云元数据；CLI 没有绕过 SSRF 或 TLS 校验的参数。

探测使用固定短输入和 `max_tokens: 32` 输出上限，仍遵守已有并发、RPS、总请求、阶段时长、单请求超时约束。只度量这种固定健康探测工作负载，不支持自定义 Prompt 或金额预算；模型费用取决于提供商。

Ctrl+C/SIGTERM 会停止本地派发并取消在途请求，之后使用独立的最多10秒窗口尝试回传 `canceled`。网页取消目前只改变服务器状态，不能即时停止已经启动的本地 CLI；请在本地终端取消。若服务器已接受网页取消，后续 CLI 回传将被拒绝，CLI 会报告未确认回传。

退出码：`0` 表示执行成功且 API 确认回传；`1` 表示执行失败或回传未确认；`2` 表示参数、授权或任务校验失败；执行期间收到取消信号且回传成功返回 `130`（使用 `go run` 时外层 Go 工具可能统一返回1并显示子进程退出码）。缺失 Key 或全部探测失败会回传白名单错误分类；部分请求失败则保留错误率。回传失败不会自动重跑目标或自动重试，以免重复产生流量和费用。

API 验证回传的有限数值、阶段与总体计数、比例、吞吐、时间窗及 SLO 一致性，但无法仅凭客户端汇总证明采样真实性；真实报告明确标注为本地 Runner 回传数据。

Runner 默认输出校验、凭据来源、执行预算、首个错误、错误聚合及回传状态。
终态 Run 的 `diagnostics` 保存实际 HTTP 状态、白名单上游错误类型/参数、网络
分类、次数、首次/末次时间与最近耗时；最多16种组合。网页运行页可查看这些字段。
例如 HTTP 400 / `unsupported_parameter` / `max_tokens` 可定位参数兼容问题。
不记录原始上游 message、响应正文、请求头、凭据或 Prompt。旧 Run 没有诊断字段，
需重新小量执行才能采集；旧分类不能用于推断具体 HTTP 状态或失败原因。

P50/P95 用全部完成样本（含错误、超时、取消）的 nearest-rank 计算。总体 P95 根据总体采样计算，不平均阶段 P95；吞吐为完成数/实际窗口秒数，错误率为错误数/完成数。每阶段少于20样本时 SLO 为 `insufficient_data`。原始样本、阶段汇总、事件均保存 SQLite；启动时将未结束运行标记为 `failed/RUN_INTERRUPTED`，不自动续跑或重复发送。

对比要求两个不同 Run 均为 `succeeded`，来源、软件版本、目标、负载及 SLO 快照相同（实验名称可以不同），并且完整执行每个配置阶段、每阶段至少20个完成样本。不可比返回409及错误码 `RUN_NOT_FINISHED`、`RUN_NOT_SUCCEEDED`、`CONFIG_MISMATCH`、`INCOMPLETE_STAGES` 或 `INSUFFICIENT_SAMPLES`；无权访问任一 Run 返回404。参数缺失、重复或比较自身返回400。

成功返回 `baseline_run_id`、`compare_run_id`、`comparable`、`stages`、`overall` 和 `notes`；每个阶段含 `stage`、`concurrency`、`baseline`、`compare` 和 `delta`。`overall` 同样包含两份原始总体指标及差值，不平均阶段 P95。`delta` 中 `p95_e2e_ms` 为毫秒差、`error_rate` 为比例差（不是百分比变化）、`throughput_rps` 为请求/秒差。比较只展示观测差异，不判断优化因果或统计显著性；共享 Mock 服务、运行时段和主机负载均可能影响结果。

## 验证与恢复

```bash
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
```

数据库采用版本化非破坏迁移，拒绝高于当前支持版本的数据库。当前只有schema v1，无降级迁移。备份应先停止服务后复制 SQLite 文件；恢复时停止服务并替换为兼容备份，同时保留签名密钥。更改签名密钥会使已有匿名会话失效。应用回滚需使用兼容schema v1的版本。

当前仍需 staging 验证；支付、登录、清理保留策略和分布式任务锁尚未实现。真实 API 仅支持本地 Runner 协议，单实例运行，禁止多个进程同时使用同一个数据库执行任务。公开部署前还需反向代理级别的访问限流和资源限制。
