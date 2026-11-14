# 测试、CI/CD 和交付

状态：`accepted`

## 测试层级

1. 单元测试：指标计算、配置校验、SLO 规则、订单状态机和 SSRF 判断。
2. 集成测试：SQLite、Mock 服务、Runner 生命周期、取消、重启恢复和 API 错误。
3. 前端测试：关键表单、状态展示、错误和空状态。
4. 端到端测试：创建实验到报告的完整 Mock 闭环。
5. staging 验证：真实构建产物、反向代理、数据库迁移、备份恢复和关键用户路径。

## CI 门禁

CI 至少执行格式化检查、静态检查、后端测试、竞态测试、前端 lint/typecheck/build/test、迁移校验和镜像构建。安全相关变更增加依赖扫描和 SSRF/权限回归测试。

## 发布步骤

```text
feature branch -> PR checks -> main -> build artifact -> staging deploy
-> smoke test -> backup -> production deploy -> health check -> observe
```

Production 发布失败时停止扩散，记录故障，回滚应用或执行已验证的数据迁移恢复步骤。

## Definition of Done

功能有实现、测试证据、文档、配置说明、日志和错误处理、资源限制、上线验证和回滚方式；没有测试证据的手工成功不能关闭任务。
