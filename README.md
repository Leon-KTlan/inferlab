# InferLab

InferLab 是一个面向 AI 推理服务的上线前体检平台：在受控预算和授权边界内运行 Mock 或真实 API 实验，输出可复核的延迟、吞吐、错误率和 SLO 报告。

当前仓库正在实现第一个线上可运营纵切面：

```text
创建 Mock 实验 -> Go Runner 真实执行 -> SQLite 持久化 -> 报告展示
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

## 容器运行

生产镜像会同时包含 Go API 和前端静态文件。先设置只存在于部署环境的会话密钥：

```bash
export INFERLAB_SESSION_SECRET="$(openssl rand -hex 32)"
docker compose -f infra/docker-compose.yml up -d --build
```

服务默认监听 `8080`。公网部署应在前面配置 HTTPS 反向代理；`infra/Caddyfile.example` 只是一份配置起点，域名和证书策略必须按 staging 验证结果填写。

## 质量检查

```bash
make check
```

真实 API、账号体系、正式支付和公网部署必须经过对应规格中的安全、staging 和回滚门禁后再启用。当前所有示例结果都必须带有 `Mock` 或示例标识。
