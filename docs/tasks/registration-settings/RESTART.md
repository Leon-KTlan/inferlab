# 4184 本地预览安全重启交接

归属：根聊天；执行聊天不运行以下备份/停止/启动命令。根已完成首次 v3 更新，保留原 DB，未创建管理员。访问 http://127.0.0.1:4184 后由用户自行初始化。

已存在的升级前备份：`/tmp/inferlab-invite-preview-DDTDMG/inferlab-pre-v3-20261119T034752Z.db`。请保留该备份和原 DB；后续备份使用新名字。

## 后续更新步骤

1. 先核对健康、监听进程、Git 版本和 DB 完整性；不要输出环境变量或读取凭据：

```bash
curl --fail --silent http://127.0.0.1:4184/healthz
lsof -nP -iTCP:4184 -sTCP:LISTEN
git rev-parse HEAD
sqlite3 /tmp/inferlab-invite-preview-DDTDMG/inferlab.db 'PRAGMA quick_check; PRAGMA user_version;'
```

2. 先构建新产物，并通过 SQLite backup 获取一致快照。不要直接复制活动 WAL 数据库，也不要覆盖唯一备份：

```bash
npm --prefix apps/web run build
preview_release=$(mktemp -d /tmp/inferlab-preview-release-XXXXXX)
go -C services/api build -o "$preview_release/inferlab-api" .
preview_backup="/tmp/inferlab-invite-preview-DDTDMG/inferlab-before-$(date -u +%Y%m%dT%H%M%SZ).db"
test ! -e "$preview_backup" && sqlite3 /tmp/inferlab-invite-preview-DDTDMG/inferlab.db ".backup '$preview_backup'"
sqlite3 "$preview_backup" 'PRAGMA quick_check;'
```

3. 核对监听 4184 的 PID 和可执行文件后，仅向该 PID 发送 TERM。通过根保存的原运行配置重启，不从命令行打印或重写任何现有秘密；原启动器需保留 SESSION_SECRET、INVITE_CODE、DAILY_REQUEST_LIMIT 等环境。以下只给非敏感覆盖参数，应在已有配置环境中执行：

```bash
APP_ENV=local LISTEN_ADDR=127.0.0.1:4184 \
  DB_PATH=/tmp/inferlab-invite-preview-DDTDMG/inferlab.db \
  WEB_DIR=/Users/leon/Downloads/infraapi/apps/web/dist \
  "$preview_release/inferlab-api"
```

使用原进程管理方式保持前台/后台，日志定向到新的本地日志；不要尝试 `killall` 或终止 8080/4173。

4. 健康检查、`PRAGMA user_version` 应为3，浏览器确认普通用户仍无设置入口、未初始化时本机显示初始化入口，已有记录保留。初始化会创建独立新管理员，不提升已有普通账户；用户需先退出当前普通账户。

## 回滚

先停止写入并保留当前 v3 DB 的新备份。旧二进制不能打开 v3；回滚必须让旧二进制使用升级前备份的另一个工作副本，不能降低 user_version、覆盖原 DB 或覆盖唯一备份。升级后的新账户和设置不会存在于旧快照中，应先决定是否接受该数据差异。云端/staging 回滚尚未演练。
