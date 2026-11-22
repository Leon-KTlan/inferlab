# 智力检测与智商雷达

状态：accepted，实施与验收记录见 docs/tasks/intelligence/。

## 产品与范围

智力检测提供创建、进度、取消、按快照复测、时间分组历史和逐题规则。智商雷达从当前身份的检测数据生成五维分布、同目标的历史及基线变化。用户手动触发检测；网页 Mock 和真实 OpenAI-compatible 本地 CLI 两种来源永久标注。v1 是公开的小型能力探针，通过率采用 0–100，不推算人类 IQ 或供应商身份。自动删除账号、周期外部检测、价格推断、自由生成代码执行不在本版范围。

## 题库与评分

套件 canary-v1：reasoning 逻辑推理、math 数学计算、instruction 指令遵循、structured 结构化输出、context 上下文检索，维度各两题。题目为项目自编的合成数据。每题重复 1–3 次，默认 2，共 20 个请求；每题二元评分。分数=通过数/计划题数×100，维度同口径。原始回答只在执行进程内评分，不持久化或回传。规则要求整个输出满足答案格式，禁止 contains 答案式评分。结构化题严格解析单个 JSON，拒绝额外字段、重复键或尾随内容。保存题目 ID、轮次、通过与否、请求耗时和白名单错误码。错误和未完成均不获得分数，未完成报告标注不可比较。

## 核心类型与文件边界

新增 IntelligenceSettings{suite_version,repetitions,mock_profile}，Mock profile 为 reference / degraded，真实目标 profile 为空。
IntelligenceProbe{id,dimension,title,prompt,expected}。IntelligenceOutcome{probe_id,repetition,passed,latency_ms,error_code?}。
IntelligenceDimension{id,label,score,passed,total,completed}。
IntelligenceReport{suite_version,score,passed,total,completed,error_count,dimensions,outcomes,complete,conclusion}。
由 intelligence_suite.go 实现 intelligenceProbes()、gradeIntelligence(probeID,content)、summarizeIntelligence(settings,outcomes)。作为通用存储兼容占位，workload 使用 [1]/30秒/1RPS/10秒单请求超时，max_requests 为计划题数；能力执行按题目序列和120秒总时限处理。
Config 增加可选 intelligence，Run 增加可选 intelligence_report。使用既有实验、Run、预留额度、归属、会话认领与原子终态回传，无架构迁移。

## HTTP 契约

全部路径前缀 /api/v1/intelligence。
GET /suite 返回 {suite_version,probes,dimensions}。
POST /runs 接收 {name,target:{type,model,endpoint?},mock_profile?,repetitions,authorized?,api_key?}。repetitions 为1–3；target mock 时 profile 默认 reference。真实需 authorized=true，遵循 HTTPS 和 SSRF 校验，api_key 可仅在内存暂存5分钟。
202 返回 {run:<原始Run>,task?:{task_id,run_id,experiment_id,source_type,config,task_token,result_endpoint,credential_available}}。
GET /runs 返回 {runs:[原始Run]}，仅 intelligence 的最近100条。
GET /runs/{id} 返回原始Run和 intelligence_report。
POST /runs/{id}/cancel 复用取消；网页取消真实任务仅关闭记录，终端 Ctrl+C 才停止目标请求。
POST /runs/{id}/rerun 接收可选 {authorized,api_key}。只允许终态，同快照重新创建新任务，真实必须再授权。
POST /runs/{id}/result 需 X-InferLab-Runner-Token。接收 {run_id,status,outcomes,started_at,finished_at,error_code?}。校验题号、重复轮次、唯一性、数量、有限耗时、终态与成功完整性，服务端从 outcomes 重算评分，禁止客户端提交总分。
GET /radar 返回 {groups:[{id,model,source_type,endpoint_label,suite_version,repetitions,runs:[原始Run],latest_run_id,baseline_run_id?,baseline_score?,delta?,signal}]}。仅已成功、无传输错误且完整的报告参与基线。至少3个历史完整运行取最近3次均值，对照最新一次，下降≥15分为 review，否则 stable，缺少历史则 insufficient_data。信号为观察规则，不能据此断言模型降智。分组包括类型、模型、endpoint、套件、重复次数和评分版本；Mock profile 变化在详情披露，仅作演示对照。

## 执行、安全与兼容

Mock 每题实际执行评分，用 reference 正确回答或 degraded 部分错误的本地 fixture，模拟来源标记醒目。Mock 不访问外部服务。真实 CLI 新子命令 intelligence-runner，任务信任原点、路径、SSRF、超时和一次性凭据沿用现有 helpers。最多30请求、单并发、最多1 req/s、10s单请求超时、120s总时限、每题512输出token上限。任务文件不含 API Key。CLI 不记录输出正文或Prompt。不执行生成代码。回传已取消任务不能改写终态。
普通性能路由拒绝 intelligence 配置/Run，不把能力测评解释为性能SLO。性能列表过滤能力实验；能力数据仍受既有活跃额度与注册认领规则约束。
公共分享与付费CSV暂拒绝能力报告，用其私有详情和题库规则复核。

## 验收与回滚

最小测试覆盖评分反例、边界输入、配额与隔离、Mock完整/取消/复跑、真实TLS fixture及回传幂等。按现有仓库命令运行一次后端与前端门禁，加关键浏览器闭环与窄屏。可用本机staging隔离验收，远端缺失配置如实记录。无需SQLite迁移，回滚前备份；旧版不执行带intelligence配置的模板。每项实现任务必须沉淀简短文档。
