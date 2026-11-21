# 红绿证据

`go test ./... -run TestDualExecutionSlowResult -count=1 -timeout=10s` 首次失败：

```text
--- FAIL: TestDualExecutionSlowResultCannotBlockCancel (0.21s)
    dual_execution_test.go:429: slow result body blocked cancellation
FAIL
```

原因：上传结果的JSON解析持有全局调度锁，慢请求阻塞取消。最小修复：在获取调度锁之前解析有大小上限的结果，随后持锁重新检查租约与终态。现同名测试证明200ms内取消完成，迟到结果409且终态保持canceled。两模式真实TLS中止测试另证明目标连接收到取消、没有后续请求。原测试与CI未删改/跳过。

最初取消测试夹具没消费HTTP请求体，导致服务端无法可靠观察断连；只修正夹具读完请求体后观察连接取消。此前因证书或浏览器依赖的失败未计为行为验证成功。下载Chromium多次网络失败后换本机Chrome，实际浏览器验收通过；不依赖未下载成功的浏览器。

最终机器可判结果在 `evidence/acceptance.json`、`browser-result.json`、`worker-isolation.txt`。阶段图表数值来自真实TLS响应采样，未伪造测量。fixture的内网/TLS注入只存在Go测试构造参数中，不提供生产绕过开关。
