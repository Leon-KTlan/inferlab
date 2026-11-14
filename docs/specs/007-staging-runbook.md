# Staging 发布运行手册

状态：`accepted`

本手册适用于已有域名和阿里云服务器的 staging 环境。真实主机、域名、镜像仓库和登录用户必须通过部署环境变量或阿里云控制台配置，不能写入仓库。

## 前置条件

- 服务器已安装 Docker Engine、Docker Compose Plugin 和反向代理。
- staging 域名已解析到服务器，HTTPS 证书可用。
- `INFERLAB_SESSION_SECRET` 已通过服务器环境变量或阿里云密钥管理服务注入，长度至少 32 字节。
- GitHub Actions 的 CI 已通过；本地 `make check`、容器构建和浏览器 smoke 已通过。

## 首次部署

```bash
git fetch --tags origin
git checkout <reviewed-commit>
export INFERLAB_SESSION_SECRET='<secret-from-secret-manager>'
docker compose -f infra/docker-compose.yml up -d --build
```

反向代理只转发到 `127.0.0.1:8080`，不直接暴露数据库目录。staging 和 production 使用不同的会话密钥、数据卷和域名。

## 发布后检查

```bash
curl --fail https://<staging-domain>/healthz
```

浏览器检查：打开首页，创建小预算 Mock 实验，确认 Run 状态从 queued/running 到终态，报告能读取阶段指标和事件。检查 `docker compose logs --tail=200 inferlab`，确认没有密钥、授权头或 Prompt。

## 备份和回滚

1. 发布前停止写入或短暂停服务，复制 SQLite 数据库到带时间戳的备份目录。
2. 保存当前镜像摘要和 Compose 配置。
3. 应用失败时恢复上一镜像并重启；数据库迁移必须保持向后兼容。
4. 恢复数据库前停止服务，保留当前失败数据副本，再替换为备份。
5. 发布后重复健康检查和 Mock smoke，记录结果。

## 生产边界

本手册不会自动执行 DNS 切换、实例删除、数据库删除或备份覆盖。生产发布可以无人值守执行，但所有破坏性基础设施变更必须在执行前确认目标、备份和回滚路径。
