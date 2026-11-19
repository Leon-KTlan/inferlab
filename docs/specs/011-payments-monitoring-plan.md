# 公开 Beta：支付、监控与运营

状态：实现与本地验证阶段；真实沙箱、staging 和生产未验收。

## 权益

首发已确认：一次购买永久解锁本人报告的 CSV 导出（爱猹收 `one_time` / `permanent`）。网页基础报告、比较与分享免费。尚未定价，未授权创建商品、上架或真实支付。

按 [爱猹收运行时契约](https://watchapay.com/agent.md)，服务端用 Bearer runtime Key 请求固定的 `https://pay.watcha.cn/v1/entitlements/access`，仅传配置的 `entitlement_id` 和会话中的稳定用户 ID。超时 3 秒，拒绝重定向，限制响应大小与请求频率。仅 `permanent + granted` 授权；`purchase_required` 可能附当次购买地址（HTTPS 或支付宝 `alipays://platformapi/startapp?...`）；未知、不可用和错误均关闭权限。购买与下载都需验证会话、报告归属和终态；每次下载重新查询，回站后可手动复查。不落地票据、Key、供应商响应或权益缓存。不接订单、回调、扣减或退款 API。

`GET /api/v1/runs/{id}/report/export-access` 返回当前状态（可选购买链接）；`GET /api/v1/runs/{id}/report.csv` 重新核验后输出固定列和数值，不导出任何用户输入、ID、端点或诊断原文，避免 CSV 公式注入。两者禁止缓存。

默认关闭。local 不允许配置真实支付；staging 只读沙箱变量；production 只读 live 变量。Key 和权益的实际环境由爱猹收账户确认，运行时不能从 Key 推断。

## 运维

独立监控进程仅探测部署方配置的 InferLab 健康和聚合接口，检查最后一次成功备份；不发模型请求。抓取接口需独立 Bearer token，仅返回固定类别的计数。通知默认停用，只在告警达到阈值和恢复时发送，故障仍持续不重复；进程重启重新建立观察窗口。

公共状态只报告当前健康取样，访问失败显示无法确认；不公布可用率、内部故障细节、抓取数据或目标。支持入口仅显示配置后的链接。条款、隐私、保留和授权声明在运营资料审核前标为草案，注册保留邀请码要求。

## 验收

本地覆盖购买前后、归属、失败关闭、监控故障及恢复、匿名状态页和移动端，并跑旧邀请与注册设置 smoke。真实环境按 [Beta 运行手册](../tasks/public-beta/RUNBOOK.md) 验收；待办见 [BLOCKED](../tasks/public-beta/BLOCKED.md)。
