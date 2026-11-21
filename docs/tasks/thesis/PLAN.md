# InferLab 本科毕设交付

基线为 public-beta 的 32ac3db。复用既有 Go、SQLite、React 与 Mock 报告。技术边界见 [可复现实验规格](../../specs/013-reproducible-mock-benchmark.md)。

| 工作 | 文件责任 | 验收 |
| --- | --- | --- |
| 阶段停止发送与在途收尾 | Runner 和阶段测试 | 低于单请求超时的跨边界请求正常完成，用户取消仍及时生效 |
| 两个固定 Mock 场景 | Mock 适配器、目标校验和场景测试 | 固定场景可识别、封存、隔离比较，旧场景仍运行 |
| 从历史快照复跑和报告字段 | Run 路由及前端入口 | 新 Run 配置与源 Run 相同，账户隔离和配额沿用现有约束 |
| 实验复现 | 隔离脚本与答辩文档 | 两组各三次，重算样本、输出图表与可复跑步骤 |

现有真实 API 授权模式继续适用。本交付基于可重复的本地 Mock，不需要外部 API Key。

## 交付验收

- 阶段收尾与取消已通过回归；[实现和口径](01-stage-boundary.md)。
- 固定场景已通过真实回环 HTTP、配置校验和快照比较测试；[场景说明](02-mock-scenarios.md)。
- 历史复跑已验证快照、账号归属和预留额度；[网页操作](03-history-and-report.md)。
- [复核工具](04-experiment-tool.md) 实际运行两组各三次；[实验结果与原始数据](RESULTS.md)。

后端 `gofmt -l .` 无输出，`go vet ./...`、`go test -count=1 ./...`、`go test -race -count=1 ./...` 通过。前端 `npm run lint`、`npm run check` 通过，含 16 项测试。以 `docker build -f infra/Dockerfile -t inferlab:thesis-117d6f9 .` 成功构建容器。本机以 `APP_ENV=staging`、临时数据库及 Caddy HTTPS 反向代理运行，Chromium 验证了未登录拒绝复跑、登录后创建两个场景、报告达标/超标、390 px 报告布局，以及新 Run 与源 Run 的快照一致。

GitHub `staging` 环境和仓库级 Actions 均未配置现有发布工作流所需的 staging 密钥，因此未执行远程 staging 部署。部署、演示与回滚见 [运行手册](RUNBOOK.md)。
