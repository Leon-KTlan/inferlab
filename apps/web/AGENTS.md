# Web 前端目录规则

- 使用 React + TypeScript + Vite，保持页面、API client、状态和展示组件边界清楚。
- 不在组件中直接拼接后端 URL；统一通过 API client 和环境配置访问 `/api/v1`。
- 所有真实数据和示例数据必须有明确来源标记，Mock 数据不能伪装成实测结果。
- 表单必须在提交前做客户端校验，服务端校验仍是最终约束。
- 页面必须覆盖 loading、empty、error、canceled 和 stale 状态，不用动画代替真实运行状态。
- 变更前端行为时补充最小单元或端到端测试，并运行 lint、typecheck、build 和相关测试。
