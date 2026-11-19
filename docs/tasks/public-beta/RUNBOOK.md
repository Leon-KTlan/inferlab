# 公开 Beta 发布运行手册

本批只允许本地验收。先保留邀请注册与免费报告链路。域名、支付商品、签约和通知渠道另行授权配置。

## 本地运行与验收

```sh
(cd services/api && go vet ./... && go test ./... && go test -race ./...)
(cd apps/web && npm ci && npm run lint && npm run check)
python3 -B scripts/ops_monitor_test.py
python3 -B scripts/public_beta_smoke.py
python3 -B scripts/stage3_smoke.py
python3 -B scripts/registration_settings_smoke.py
```

浏览器 smoke 需要 Python Playwright 和 Chromium；沿用 CI 中的安装版本。脚本新建临时数据库和随机端口，真实模型、平台沙箱和正式支付均不参与。手动预览时将生产前端构建目录设为 `WEB_DIR`，将 `DB_PATH` 指向新路径，`LISTEN_ADDR` 绑定独立的 loopback 端口；通过工作台底部链接或 `/status`、`/policies` 查看。

## 配置与监控

- Compose >= 2.24 支持可选的部署根目录 `.env.beta`；文件权限设为 0600，仅在服务器经安全管道配置，不进入 Git 或镜像。环境隔离见 [staging 手册](../../specs/007-staging-runbook.md)。staging workflow 设置 `INFERLAB_APP_ENV=staging`，手动 staging 部署也必须在主 `.env` 设置该项。正式环境使用独立部署目录及默认 `production`。
- `PAYMENTS_ENABLED` 缺省关闭。授权后 staging 仅注入 `WATCHA_SANDBOX_API_KEY`、`WATCHA_SANDBOX_ENTITLEMENT_ID`；生产仅注入 `WATCHA_LIVE_API_KEY`、`WATCHA_LIVE_ENTITLEMENT_ID`。使用运行时 Key，勿注入 Agent 凭证；环境确认以平台控制台为准。每实例每分钟至多发起 60 次权益查询、同时最多 4 次。
- `PUBLIC_SUPPORT_URL` 可选，填写运营方确认的公开 HTTPS 支持地址，不含票据或凭据；缺省显示暂未配置。条款、隐私、主体、适用地区、数据与备份保留期需在公开前审查。
- 私有抓取设置随机长 `OPS_METRICS_TOKEN`，公网反代屏蔽 `/internal/*`。同机监控使用应用的 loopback 端口；从独立机器做外部健康探测时，使用经授权的已验证 HTTPS 域名且不配置抓取 token。只用部署方固定配置的应用地址，不将用户输入当作探测目标。
- 以独立于应用容器的进程服务运行 `python3 -B scripts/ops_monitor.py`，例如主机进程管理器；设 `OPS_BASE_URL` 为应用 origin。可另设 `OPS_BACKUP_DIR` 为实际备份目录，授予监控用户读取 `.last-success` 元数据所需的最小权限。部署脚本只在备份成功后更新时间标记；定时备份任务也只在快照成功后更新标记。部署时备份不能替代定时备份。
- 缺省每 60 秒检查一次、备份超过 24 小时报警（`OPS_INTERVAL_SECONDS`、`OPS_MAX_BACKUP_AGE_SECONDS`）。健康失败、抓取失败、备份过期，或每检测间隔 HTTP 5xx ≥ 5 / 失败任务 ≥ 3 / 支付失败 ≥ 3，连续命中两次才触发；连续两次正常后恢复。重启重新建立基线，不保留历史，故障检测和汇总仅止于所在实例。
- 缺省仅记事件类型与状态，不外发。经授权配置 `OPS_WEBHOOK_URL` 与可选 `OPS_WEBHOOK_TOKEN` 后，接收端需接受 JSON `{"check":"health","state":"firing"}`（恢复为 `resolved`）并返回 2xx。不记录 URL、认证头、请求内容或接收者细节。仅需服务器的应用部署、只读备份标记和授权的通知通道权限。

## 发布验证顺序

1. 确认独立的 staging 数据库、域名和密钥。核对公开域名、TLS 证书、可信 HTTPS 代理和 loopback 绑定；不要信任用户提供的转发头。实际测试同源、登录、共享代理 IP 下的限流、隔离、邀请注册、Mock 报告及其分享撤销。确认主体、备案/适用地区、支持入口、隐私和数据删除流程。
2. 核对部署镜像 digest，备份 SQLite 与存在的 WAL/SHM，把快照恢复到隔离环境做读取验证。检查健康、抓取认证和公开状态。用已授权的测试通知接收器做故障和恢复演练；用容器外探测验证应用整体停机可见。核对备份时效与最新成功标记。
3. Owner 确认价格并另行授权平台操作后，在 [爱猹收控制台](https://pay.watcha.cn/console/account) 核对沙箱商品为 `one_time`、权益为 `permanent`。只在 staging 注入沙箱配置。新测试账号先查询得到购买入口，Owner 在该账号的托管链接完成沙箱模拟购买，随后重新查同一账号并下载本人 CSV。另测跨账号和关闭开关；本地 fake provider 不计为此验收。
4. 独立审查正式开通、支付宝签约和渠道审核。商品控制台的渠道状态必须实际为 active、档位匹配，填写真实公网应用地址；分别核对档位金额上限与商品交易限额，未知限额注明未核实。先用 live 环境做新账号未购买核验，经明确授权由非收款方买家完成一次测试购买并复查 CSV。上线前明确退款入口、操作责任、退款后权益是否撤销以及复查流程；本服务没有退款、订单或回调接口。
5. 生产发布以备份、回滚演练、检查结果和外部验证为前置条件。没有授权和生产证据时保持收费关闭和邀请注册。

## 回滚

紧急关闭收费：关闭 `PAYMENTS_ENABLED` 后重建容器，验证基础报告、比较与分享可用；权益由平台持有，不清理。监控可独立停止或撤掉 webhook 配置。阶段 4 无数据库迁移，数据仍是 PR #7 的 schema v3；回退阶段 4 时恢复 PR #7 对应镜像和 Compose 文件，保留同一 v3 数据卷，不覆盖历史。回滚到比 PR #7 更早的 schema 按 spec 012 执行。保留失败镜像/数据库副本；确需恢复备份按 spec 007 手工核对并先在隔离环境演练。
