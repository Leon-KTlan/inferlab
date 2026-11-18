# InferLab

InferLab 是一个面向 AI 推理服务的上线前体检平台：在受控预算和授权边界内运行 Mock 或真实 API 实验，输出可复核的延迟、吞吐、错误率和 SLO 报告。

当前支持两条实验路径，公网发布仍待 staging 验证：

```text
创建 Mock 实验 -> Go Runner 真实执行 -> SQLite 持久化 -> 报告展示
创建 OpenAI-compatible 任务 -> 下载 task.json -> 本地 Runner 调用目标 -> 回传脱敏报告
```

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
账号体系、正式支付和公网部署尚未完成。所有功能仍须经过对应规格中的 staging
和回滚门禁后才可标记上线完成，Mock 与客户端上报结果必须准确标注来源。
