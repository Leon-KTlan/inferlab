# 双执行本地与发布操作

状态：本地受控 TLS 验收通过，待主架构审查/staging；无云端变更。所有命令从本任务工作树根目录开始。禁止用现有供应商 Key 替换测试夹具的虚构 Key。

## 一条命令验收

```bash
BROWSER_PYTHON=/tmp/inferlab-dual-browser/bin/python scripts/dual_execution_acceptance.sh
```

需要 Go1.24+、Node22/24+、Python Playwright、本机 Chrome 和 Docker。浏览器脚本只使用测试编译标签下的 TLS fixture；例外只通过测试构造函数注入，无任何生产放开内网/忽略TLS的选项。正常接口→独立Worker/Agent CLI→真正TLS上游请求→报告与图表，没有模拟执行器或合成测量。截图写入 evidence/，不保存领取码/Key。不具备浏览器依赖时可先执行脚本中的Go/前端门禁；不得把未执行的步骤算通过。

本地普通预览（平台默认关闭，本地Agent可针对用户授权的公网HTTPS目标使用）：

```bash
scripts/dual_execution_preview.sh
```

脚本自动构建并启动临时数据库和前端，打印本机网址；退出清理临时目录。使用已有匿名本地工作区，无需云资源。平台关闭会明确显示“平台代跑未配置”，手动选择本地Agent后才提交；生产仍强制账户登录。两模式受控假上游预览由 `scripts/dual_execution_acceptance.sh` 自动操作并留下截图，不可用真实工具的安全策略去访问fixture私网。

高级已安装工具入口（具体任务由页面自动生成，用户无需替换文件路径）：

```bash
cd services/api
go build -trimpath -o /tmp/inferlab-dual .
/tmp/inferlab-dual agent --api-url http://127.0.0.1:8080 --authorize-target
```

该进程从stdin读取网页本次的私人JSON，不应将JSON放入命令参数、shell here-doc、日志或文件。推荐直接复制整段给Agent，Agent通过进程API输入stdin。官方固定源码版本与归档SHA-256见前端 `runner-release.ts`；Go缺失时，任务指令引导临时安装固定官方Go1.24.13并核验官方摘要。工具失败后不自动重跑；用户须重新确认预算创建新实验。每次复制授权5分钟、单次领取；刷新Run/报告可恢复，领取码不可恢复。

## 平台独立 Worker

API需配置（值从环境/密钥管理注入）：`PLATFORM_EXECUTION_ENABLED=true`、`PLATFORM_WORKER_TOKEN`（随机至少32字节）、`PLATFORM_LOCATION`（真实测量机地域标签）、`PLATFORM_ISOLATION_CONFIRMED=true`。缺一则拒绝启动；平台默认关闭，不静默改为本地。只允许单API实例，不共享数据库运行多个调度器。硬上限在013；UI快速默认并发1/2、阶段10秒、RPS2、40请求，单请求10秒。平台最多1个（含排队），同用户/同规范化目标各1，继承全局4和日额度；已预留请求不因取消/失败退还。

必须先验证容器隔离，再设置确认值。此确认只是操作记录，不会自动建立隔离：

```bash
docker build -f infra/worker.Dockerfile -t inferlab-dual-worker:local .
docker run --rm --read-only --tmpfs /tmp --security-opt no-new-privileges \
  --cap-drop ALL --cap-add NET_ADMIN --cap-add SETUID --cap-add SETGID --cap-add SETPCAP \
  -e CONTROL_IP=203.0.113.99 -e CONTROL_ORIGIN=https://control.example \
  inferlab-dual-worker:local --verify-only
```

以上测试控制地址是文档保留IP，不访问真实服务。探针真实向loopback、宿主网桥和metadata发送连接并要求DROP计数命中，随后证明Uid/Gid65532及CapEff/CapBnd为0。生产给独立TLS控制服务配置真实IPv4地址和证书域名，然后通过运行环境提供 `CONTROL_IP`、`CONTROL_HOST`、`CONTROL_ORIGIN` 与同一 `PLATFORM_WORKER_TOKEN`：

```bash
docker compose -f infra/worker-compose.yml up --build -d
```

只允许公网IPv4 TCP443、Docker DNS和指定控制IP:443；IPv6全部禁用。非443/IPv6目标应选择本地模式。容器不挂宿主目录、DB或Docker socket，不能使用host network/privileged；启动时仅在自身网络命名空间设置规则，执行前降权并清空capabilities。指定控制IP是唯一可能的内部网络例外，必须是专用API控制服务，不能设为宿主敏感服务。允许列表变动须重新执行隔离探针。宿主/同网段其他IP仍被拒绝。镜像启动本身不证明具体staging网络隔离，须在staging重复探针、TLS与真实控制路径验收。

独立裸进程命令为 `go run . worker --api-url HTTPS_CONTROL_ORIGIN`（在services/api）；只适用于已由管理员提供同等网络隔离的主机。未隔离的裸进程不能作为平台启用证据。Worker每次启动新实例ID，只取得平台任务，结果/控制使用本次随机lease，不共享本地task令牌。API或Worker重启不重放原任务；3秒失联或硬期限失败，要求用户重新提交。

## 最小 Nginx 代理

现有staging阻断所有runner-tasks；不修改这条阻断也可以开放新Agent协议。`infra/dual-execution-nginx.conf.example` 分别给出 http层限流zone、公共站点两条精确匹配路由，以及独立TLS控制站点。仅新增runner-claims和规范化Run ID的execution-leases/control|result，禁止暴露整个runner-tasks或internal前缀。其余experiments/executions、capabilities走原已鉴权API代理。保留Host，不信任转发IP；API失败桶仅限制无效领取，有效单次码通过任务和账户校验后不受共享失败桶阻断；代理级按客户端限流仍须staging验收。静态站点与API必须同源。控制站点仅允许Worker出口IP，仍需Worker身份令牌；不要把internal路由加到公共站点。控制与领取响应禁缓存、禁请求体/敏感头日志。

## 配置、回滚、未决

无新增schema迁移；Run JSON新增execution，旧版读取器可忽略字段。旧手动协议保留，旧历史报告可读；旧-旧比较保留原语义并提示位置未知，新-旧/不同来源/地域拒绝直接比较。新本地Run默认带本次观测ID，不声称跨Run相同测量机。

回滚：关闭平台开关并停止独立Worker；已有Run会取消/失联失败，绝不自动重跑。再回滚本PR代码并重建前端/API；无数据回滚需求，部署前仍按既有规则备份DB。前端状态恢复只存Run ID，Key/领取码/lease均不写浏览器存储。

待staging：邀请账户、HTTPS代理两新路由、Worker专用TLS控制链路、隔离探针、实际地域标记、取消与刷新报告。真实供应商另需用户明确Endpoint/模型/预算；本次未调用、未部署、未合并。
