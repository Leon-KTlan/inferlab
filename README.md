# InferLab

InferLab 是一个面向 AI 推理服务的上线前体检平台：在受控预算和授权边界内运行 Mock 或真实 API 实验，输出可复核的延迟、吞吐、错误率和 SLO 报告。

当前支持两条实验路径，公网发布仍待 staging 验证：

```text
创建 Mock 实验 -> Go Runner 真实执行 -> SQLite 持久化 -> 报告展示
创建 OpenAI-compatible 任务 -> 下载 task.json -> 本地 Runner 调用目标 -> 回传脱敏报告
```

邀请测试现已实现本地流程：邀请注册/登录、七天可撤销会话、账户数据隔离、每日请求额度预留、终态脱敏报告分享/撤销、实验与运行删除。尚未通过 staging 验收，不能作为已上线服务使用。

## 目录

```text
apps/web/       React + TypeScript + Vite 前端
services/api/  Go API、SQLite 和实验 Runner
infra/          容器与部署基线
docs/specs/     已确认的产品和技术规格
```

先阅读根目录 `AGENTS.md`，再阅读对应目录下的作用域规则。产品范围见 `InferLab_PRD_v0.1.md`，阶段计划见 `InferLab_Roadmap_v0.2.md`。

## 本地运行

环境要求：Go 1.24+、Node.js 20+、npm 10+。

终端一：

```bash
make api-run
```

API 默认监听 `http://localhost:8080`，SQLite 默认写入 `services/api/data/inferlab.db`。可用 `LISTEN_ADDR`、`DB_PATH`、`WEB_DIR`、`APP_ENV` 和 `SESSION_SECRET` 覆盖默认配置。

终端二：

```bash
make web-install
cd apps/web
npm run dev
```

前端默认监听 Vite 地址，并将 `/api` 代理到 `http://localhost:8080`。

## 邀请测试配置

在 API 进程环境中提供 `INVITE_CODE` 才开启邀请注册，未配置则关闭；请通过环境管理工具注入，不写入仓库或日志。`DAILY_REQUEST_LIMIT` 为每身份 UTC 每日预留上限，默认 10000。密码使用 bcrypt，长度 12–72 字节，不截断。邀请值、密码和会话 token 不存入浏览器存储；会话使用 HttpOnly cookie。

local 允许匿名试用，staging/production 启动 Mock 或创建本地 Runner 任务须登录。注册可原子认领当前匿名实验、活跃任务和累计额度一次；登录已有账户不会合并匿名数据，并使原匿名会话失效。请在切换账户前保存需要的匿名报告。当前没有密码重置、邮件或账户找回。

额度按任务的 `max_requests` 预留，Mock 和本地 Runner 共用；启动后失败、取消、删除均不退还。它不是供应商账单，也不能限制用户额外在本地发起的请求。分享仅含数值指标及 SLO，任何持有链接的人都可只读访问，直至撤销或删除。名称、模型、endpoint、事件、诊断、内部 ID 和凭据不进入公共响应。

反向代理须保留原 Host；写 API 校验同源 Origin，身份写接口必须使用 `application/json`。登录/注册在 KDF 前以实际连接 IP 限制十分钟五次尝试，登录另限制用户名；不信任 XFF，代理后的用户可能共享 IP 桶，须在 staging 验证实际拓扑。限流持久化并限制桶数量，过期会话/桶分批清理。

SQLite 会自动从 v1 事务迁移到 v2并保留记录。部署前用 SQLite backup 备份，停止写入后部署；启动时原有未结束任务仍按既有规则标记中断，不会重复执行。回滚须恢复迁移前数据库备份与旧二进制，不能直接降低 `user_version`。详见 `docs/specs/010-invite-testing.md`。

## 本地执行真实 API 任务

在网页选择 OpenAI-compatible，填写已授权的 HTTPS 服务地址和模型，创建并下载
`task.json`。可在 Endpoint 旁填写 API Key，创建后5分钟内启动 Runner；无需终端
输入 Key。留空时仍可用本地 `OPENAI_API_KEY`。从 `services/api` 目录运行：

```bash
go run . runner --task /path/task.json --api-url http://127.0.0.1:4173 --authorize-target
```

将 `--api-url` 改成实际 InferLab 网页的 origin；非本机服务必须使用 HTTPS。
网页输入的 Key 仅在服务端内存暂存5分钟并由 Runner 领取一次，不写入任务文件、
数据库或浏览器存储。任务文件含本次运行的领取和回传凭据，请妥善保管。
运行后回到原浏览器查看报告。本地按 Ctrl+C 可停止请求；网页取消只更新服务端
状态，当前不能远程停止本地进程。

真实 API 报告为本地客户端上报结果。请求数、输出和时间有限制，但当前没有精确
金额预算或实际 token 吞吐统计。协议与验收边界见 `docs/specs/009-local-runner-cli.md`。

## 容器运行

生产镜像会同时包含 Go API 和前端静态文件。先设置只存在于部署环境的会话密钥：

```bash
export INFERLAB_SESSION_SECRET="$(openssl rand -hex 32)"
docker compose -f infra/docker-compose.yml up -d --build
```

服务默认绑定 `127.0.0.1:8080`，可用 `INFERLAB_HTTP_PORT` 调整宿主机端口。
公网部署应在前面配置 HTTPS 反向代理；`infra/Caddyfile.example` 只是一份配置起点，域名和证书策略必须按 staging 验证结果填写。

## 质量检查

```bash
make check
```

真实供应商测试需要具体目标、模型、授权和预算；本地集成测试使用 fake TLS 目标。
正式支付、监控和公网部署尚未完成；后续依赖见 `docs/specs/011-payments-monitoring-plan.md`。所有功能仍须经过对应规格中的 staging
和回滚门禁后才可标记上线完成，Mock 与客户端上报结果必须准确标注来源。

邀请测试本地验收（临时 DB、随机空闲端口，不访问真实供应商）：

```bash
cd apps/web && npm run lint && npm run check
cd ../../services/api && go vet ./... && go test ./... && go test -race ./...
cd ../.. && python3 scripts/stage3_smoke.py
python3 scripts/stage3_smoke.py --legacy
```

需要 Python Playwright 和 Chromium。新 smoke 自建临时 API，验证真实注册→Mock→分享→独立访客→重启→撤销→配额→删除→登录恢复，以及延迟身份响应场景。`--legacy` 使用原 `scripts/ui_smoke.py`，通过测试进程内 HTTP 转发 fixture 将其固定 4173 origin 指向隔离 API；原脚本与断言不改，也不访问现有 8080/4173 服务。
