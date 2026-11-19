# 站点初始化与注册管理

状态：`implementing`；依赖：PR #6 的邀请注册链路。

## 目标与范围

浏览器首次初始化独立管理员账户；管理员配置邀请注册；普通访客按有效邀请码注册并自动登录。现有账户统一迁移为普通用户，不自动提权，不修改实验、配额或既有数据归属。不包含找回密码、邮件、复杂 RBAC 或多管理员管理。

## 数据与接口

- SQLite schema v3：users.role 默认为 user，仅允许 user/admin；唯一管理员索引；单行 registration_settings 保存 enabled 和邀请码 SHA-256 摘要；admin_audit 仅保存操作、操作者 ID、时间和 enabled。
- GET /api/v1/auth/me 增加 role、setup_required；registration_open 使用有效配置。
- POST /api/v1/auth/setup 接受 username/password/setup_token，事务中检查无管理员，创建全新管理员、轮换会话并记录审计；重复用户名不提权且完整回滚。已登录身份须先退出。
- GET/PUT /api/v1/admin/registration 仅管理员可用，其它身份均 403。GET 仅返回 enabled、invite_configured、source；PUT 接受 enabled 和可选 invite。空 invite 保留现有摘要；开启必须有有效摘要。
- DB 尚无配置时回退 INVITE_CODE；首次保存后始终以 DB 为准。关闭保留摘要，换码立即使旧码失效。注册事务内复核有效配置。

## 安全约束

初始化限 JSON、同源、每真实 RemoteAddr 每十分钟最多五次，先限流后 bcrypt。密码 12-72 UTF-8 字节；管理员新邀请码 8-256 UTF-8 字节且不可全空白或含控制字符，旧 INVITE_CODE 不追加新约束。部署令牌仅通过请求 JSON 临时传送，不返回、不持久化、不记录。

仅 APP_ENV 为空或 local 且 secure=false、RemoteAddr 是 loopback、精确解析 Host 是 localhost 或 loopback IP、且无 Forwarded/X-Forwarded-* 请求头时允许无令牌初始化。staging/production/其它非 local 环境始终要求 ADMIN_SETUP_TOKEN，缺失或不匹配拒绝。令牌比较使用固定长度摘要。

auth/me 仅向满足上述本地条件的请求返回 setup_required=true；公网不能探测无管理员状态。登录框始终提供显式初始化入口，非本地入口需填写部署令牌。本地首次访问另显示初始化提示。确认密码仅在前端内存。

Vite 开发代理必须保留原始 localhost Host，不能添加转发头；默认推荐 Go 静态服务直接同源访问。远程代理或隧道请配置 ADMIN_SETUP_TOKEN。API 不信任 XFF 来判定来源。

## 验收与回滚

API 测试覆盖迁移保留数据、普通用户无管理权限、Host/Origin/转发头伪造、secure 令牌、并发一次性初始化、失败回滚、限流、配置优先级及重启持久化。真实浏览器从无管理员无邀请码数据库验证完整流程、窄屏、键盘、错误与等待状态；旧 smoke 断言保持不变。

仅使用临时数据库、随机本地端口、Mock，不触碰 8080/4173/4184 服务。升级前备份 SQLite；降级旧二进制必须恢复升级前备份，旧版本不支持 v3。部署需另行记录备份、健康检查、回滚和 staging 证据。

## 未决事项

独立验收和 staging 尚未执行；CI workflow 由根聊天负责增加新 smoke。
