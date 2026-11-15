# Staging 发布运行手册

状态：`accepted`

本手册适用于已有域名和阿里云服务器的 staging 环境。真实主机、域名、镜像仓库和登录用户必须通过部署环境变量或阿里云控制台配置，不能写入仓库。

## 前置条件

- 服务器已安装 Docker Engine、Docker Compose Plugin 和反向代理。
- staging 域名已解析到服务器，HTTPS 证书可用。
- `INFERLAB_SESSION_SECRET` 已通过服务器环境变量或阿里云密钥管理服务注入，长度至少 32 字节。
- GitHub Actions 的 CI 已通过；本地 `make check`、容器构建和浏览器 smoke 已通过。

GitHub `staging` Environment 需要配置：`STAGING_SSH_HOST`、`STAGING_SSH_USER`、`STAGING_SSH_PRIVATE_KEY`、`STAGING_KNOWN_HOSTS`、`STAGING_DEPLOY_DIR`、`INFERLAB_SESSION_SECRET`、`STAGING_REGISTRY_USER` 和 `STAGING_REGISTRY_TOKEN`。私钥和 token 只存 GitHub Secrets，主机指纹必须人工核对后写入 `STAGING_KNOWN_HOSTS`。

### 配置 GitHub Environment

在已登录并指向本仓库的 `gh` CLI 中，可以先创建环境，再逐项从交互输入或安全管道写入 secrets；命令不会把 secret 值写进 shell 历史：

```bash
repo=Leon-KTlan/inferlab
gh api --method PUT "repos/$repo/environments/staging" --silent
gh secret set STAGING_SSH_HOST --env staging
gh secret set STAGING_SSH_USER --env staging
gh secret set STAGING_SSH_PRIVATE_KEY --env staging
gh secret set STAGING_KNOWN_HOSTS --env staging
gh secret set STAGING_DEPLOY_DIR --env staging
gh secret set INFERLAB_SESSION_SECRET --env staging
gh secret set STAGING_REGISTRY_USER --env staging
gh secret set STAGING_REGISTRY_TOKEN --env staging
```

配置后只检查 secret 名称，不读取 secret 值：

```bash
gh api "repos/$repo/environments/staging/secrets" --jq '.secrets[].name'
```

输出包含上述 8 个名称后，再通过 GitHub Actions 的 `Deploy staging` 手动运行；工作流会在构建前验证绝对部署目录和会话密钥长度。

## 首次部署

```bash
git fetch --tags origin
git checkout <reviewed-commit>
export INFERLAB_SESSION_SECRET='<secret-from-secret-manager>'
docker compose -f infra/docker-compose.yml up -d --build
```

有完整 GitHub Environment 配置后，合并到 `main` 会自动触发 `Deploy staging`；也可以通过 `workflow_dispatch` 手动运行。工作流按当前 commit 的 SHA 构建镜像，并将构建产物的 digest 写入远端 `.env`，不会使用可变的 `staging` 镜像标签。同一 staging 环境使用并发锁，部署脚本会保存旧镜像为本地 rollback tag，并在健康检查或 Mock smoke 失败时回滚到该镜像。

工作流在构建镜像前校验所有必需 secrets、绝对部署目录和会话密钥长度；配置不完整时直接失败，不发布镜像也不连接服务器。

反向代理只转发到 `127.0.0.1:8080`，不直接暴露数据库目录。staging 和 production 使用不同的会话密钥、数据卷和域名。

## 发布后检查

```bash
curl --fail https://<staging-domain>/healthz
```

部署脚本还会在服务器本机执行 Mock API 闭环：创建实验、启动 Run、轮询到 `succeeded`，并读取报告接口。浏览器检查仍需打开首页，确认 Run 状态从 queued/running 到终态，报告能读取阶段指标和事件。检查 `docker compose logs --tail=200 inferlab`，确认没有密钥、授权头或 Prompt。

## 备份和回滚

1. 发布前 `deploy-environment.sh` 停止当前容器，在 `$DEPLOY_DIR/backups/` 保存带 UTC 时间戳的 SQLite 主库及存在的 `-wal`/`-shm` 文件。
2. 保存当前镜像的本地 rollback tag；新镜像使用 commit 对应的 registry digest。
3. 应用健康检查或 Mock smoke 失败时，先停止失败容器并保存 `failed-<timestamp>.db` 副本，再恢复上一镜像并重启。
4. 回滚不会自动覆盖数据库；恢复数据库前必须停止服务、保留失败副本，并人工确认要恢复的备份文件。
5. 发布后记录 `/healthz`、Mock smoke、镜像 digest 和备份路径；数据库迁移必须保持向后兼容。

## 生产边界

本手册不会自动执行 DNS 切换、实例删除、数据库删除或备份覆盖。生产 workflow 仅支持手动触发，绑定 GitHub `production` Environment，并使用独立的 `PRODUCTION_*` secrets：`PRODUCTION_SSH_HOST`、`PRODUCTION_SSH_USER`、`PRODUCTION_SSH_PRIVATE_KEY`、`PRODUCTION_KNOWN_HOSTS`、`PRODUCTION_DEPLOY_DIR`、`INFERLAB_PRODUCTION_SESSION_SECRET`、`PRODUCTION_REGISTRY_USER` 和 `PRODUCTION_REGISTRY_TOKEN`。生产发布可以无人值守执行，但所有破坏性基础设施变更必须在执行前确认目标、备份和回滚路径。

Production workflow 还强制 `github.ref` 为 `main`，并要求手动输入确认词 `DEPLOY_PRODUCTION`；即使有人从其它分支或误点 workflow，也不会发布未合并代码或意外触发生产发布。
