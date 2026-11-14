# InferLab API

本地仅开放 Mock，所有指标来自进程内本地 HTTP 模拟服务的真实请求采样。

```bash
go run .
```

默认监听 `127.0.0.1:8080`。`GET /healthz` 检查数据库连通性。

配置环境变量：`LISTEN_ADDR`（监听地址）、`DB_PATH`（默认 `data/inferlab.db`）、`WEB_DIR`（可选，前端构建目录）、`APP_ENV`（local/staging/production）、`SESSION_SECRET`（签名密钥）。生产必须提供至少32字节的 `SESSION_SECRET` 并通过 HTTPS 反向代理访问；local/staging 未配置时在数据库保存随机签名密钥，以保持重启后的匿名会话。

## API

浏览器采用 HttpOnly、SameSite=Strict 签名 cookie 隔离匿名访问者，API 为同源访问，不开放 CORS。丢失 cookie 将无法读取原实验；当前没有账号、会话找回或共享报告。

- `POST /api/v1/experiments` 创建配置（见下例）。
- `GET /api/v1/experiments` 返回当前访问者最近100个实验：`{experiments:[...]}`。
- `GET /api/v1/experiments/{id}` 读取配置。
- `POST /api/v1/experiments/{id}/runs` 启动实验，202 返回 Run。
- `GET /api/v1/runs` 返回当前访问者最近100次 Run，用于刷新后恢复工作台。
- `GET /api/v1/runs/{id}` 读取持久化状态、配置快照和实时指标。
- `POST /api/v1/runs/{id}/cancel` 异步幂等取消，继续轮询确认终态。
- `GET /api/v1/runs/{id}/events?after=0` 按事件ID查询，单次最多200条。
- `GET /api/v1/runs/{id}/report` 终态报告，运行中返回409。

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

P50/P95 用全部完成样本（含错误、超时、取消）的 nearest-rank 计算。总体 P95 根据总体采样计算，不平均阶段 P95；吞吐为完成数/实际窗口秒数，错误率为错误数/完成数。每阶段少于20样本时 SLO 为 `insufficient_data`。原始样本、阶段汇总、事件均保存 SQLite；启动时将未结束运行标记为 `failed/RUN_INTERRUPTED`，不自动续跑或重复发送。

## 验证与恢复

```bash
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
```

数据库采用版本化非破坏迁移，拒绝高于当前支持版本的数据库。当前只有schema v1，无降级迁移。备份应先停止服务后复制 SQLite 文件；恢复时停止服务并替换为兼容备份，同时保留签名密钥。更改签名密钥会使已有匿名会话失效。应用回滚需使用兼容schema v1的版本。

当前仍需 staging 验证；支付、真实 API、本地外部 Runner、登录、报告对比、清理保留策略和分布式任务锁尚未实现。单实例运行，禁止多个进程同时使用同一个数据库执行任务。公开部署前还需反向代理级别的访问限流和资源限制。
