# LLM 度量闭环 · 第一阶段任务清单

对应:[plan](2026-11-07-llm-metrics-eval-plan.md) · [design](2026-11-07-llm-metrics-eval-design.md)

> 粒度说明:任务 4 为跨 7 文件的**原子签名变更**(ChatJSON/StreamChat 加 scene、
> chatOnce 链路返回 usageInfo),中间态不可编译,无法按「≤3 文件」拆分,故整体为一个任务。

### Wave 1(无依赖,可并行)

- [ ] **任务 1:创建 LlmUsage 模型与场景枚举**
  | 涉及文件: `server/internal/model/llm_usage.go`(新增)、`server/internal/repo/db.go`(修改)
  | 验收: `task build` 过;启动后 llm_usage 表自动创建,含 `(scene, created_at)` 复合索引;`model.LlmScene` 类型化常量含 analyze / write_article / rag_chat / embedding / judge / refigure 六值;字段与 plan 数据契约一致(无预留字段、无软删)

- [ ] **任务 2:新增 pricing 配置**
  | 涉及文件: `server/internal/config/config.go`、`server/config.yaml`
  | 验收: `LLM.Pricing map[string]LLMPrice` 结构体与 yaml 键名对齐(键=模型名);价目为示例值且注释标明按厂商最新价目核对;Pricing 为空 map 时成本计算按 0 处理不 panic;`.env.example` 有意不加(plan 已明示)

- [ ] **任务 3:补齐 usage 解析字段**
  | 涉及文件: `server/internal/llm/types.go`、`llm/embedding.go`(仅 embedResponse 结构体,该结构定义于此而非 types.go;新增单测 `usage_test.go`)
  | 验收: `chatResponse` 解析 `prompt_tokens/completion_tokens`,`anthropicResponse` 解析 `input_tokens/output_tokens`,`embedResponse` 解析 `prompt_tokens/total_tokens`;定义 `usageInfo`;`go test ./internal/llm/` 三格式解析用例全过;**不改任何函数签名**(纯加字段)

### Wave 2(依赖任务 1、3)

- [ ] **任务 4:改造 ChatJSON/StreamChat 签名并接入全部调用点**(原子变更,7 文件)
  | 涉及文件: `llm/client.go`、`llm/client_anthropic.go`、`llm/stream.go`(仅签名与分发)、`task/pipeline.go`、`service/article.go`、`service/rag.go`、`app/bootstrap_pipeline.go`
  | 依赖: 1, 3
  | 验收: `task vet` + `task build` 过;全部 5 个调用点传 scene——4 处 ChatJSON(pipeline analyze / pipeline write_article / article refigure / rag 改写)+ 1 处 StreamChat(rag 流式回答),均传 rag_chat 之外的对应场景、rag 两处传 rag_chat;`Client` 加 `db *gorm.DB`(nil 跳过,与 rdb 同模式);`recordUsage` 在 chatWithRetry 物理请求粒度落库,insert 失败仅 Warn;本地跑一次 RAG 问答,rag_chat 行落库且非流式行 token > 0

- [ ] **任务 5:实现 MetricsService 聚合与成本计算**
  | 涉及文件: `server/internal/service/metrics.go`(新增,单测 `metrics_test.go`)
  | 依赖: 1, 2
  | 验收: `Summary(ctx)` 返回今日/本月成本、总 token、调用数、平均耗时、失败率;`Daily(ctx, days)` 按日×场景 GROUP BY 聚合;成本按 Pricing 查表、模型缺失返回 0;`go test ./internal/service/` 成本计算纯函数用例过

### Wave 3(依赖 Wave 2)

- [ ] **任务 6:流式响应 usage 解析**
  | 涉及文件: `server/internal/llm/stream.go`、`llm/stream_anthropic.go`
  | 依赖: 4
  | 验收: OpenAI 流式请求体加 `stream_options.include_usage` 并解析末帧 usage;Anthropic 流式累积 `message_start.usage.input_tokens` 与 `message_delta.usage.output_tokens`;解析不到记 0 不报错;stream.go:14「流式不计入用量」过时注释更新;`go test ./internal/llm/` 过

- [ ] **任务 7:Embedder 埋点**
  | 涉及文件: `server/internal/llm/embedding.go`、`app/bootstrap_pipeline.go`(NewEmbedder 补 db 实参)
  | 依赖: 1, 3(装配文件与任务 4 相同,文件避让置于本 Wave)
  | 验收: `Embed` 成功/失败均落 llm_usage(scene=embedding,completion=0,prompt=usage.prompt_tokens);发布一篇文章触发切块向量化后 embedding 行落库;db=nil 跳过

- [ ] **任务 8:看板接口上线**
  | 涉及文件: `server/internal/api/metrics_handler.go`(新增)、`router/router.go`、`cmd/server/main.go` 或 `app/` 装配点(Deps 加 Metrics)
  | 依赖: 5
  | 验收: admin JWT 下 `GET /api/v1/metrics/summary` 与 `GET /api/v1/metrics/daily?days=30` 返回 JSON;handler 薄(参数绑定→service→resp,无业务逻辑);未登录 401

### Wave 4(收尾)

- [ ] **任务 9:admin「数据」页面**
  | 涉及文件: `web/admin/src/types/metrics.ts`(新增)、`pages/Metrics.tsx`(新增)、`router/config.tsx`
  | 依赖: 8
  | 验收: `npm run build` 过;侧边菜单出现「数据」;Statistic 卡片(今日/本月成本、总 token、调用数、平均耗时、失败率)与按日×场景表格渲染真实数据;请求走 `api/http.ts`,类型对齐 `types/metrics.ts`;函数组件无 any

- [ ] **任务 10:端到端验证闭环**
  | 涉及文件: 无新增(仅验证;发现缺陷回填对应前置任务)
  | 依赖: 4, 6, 7, 8, 9
  | 验收: `task vet` + `task build` + `go test ./...` + admin/blog `npm run build` 全过;完整链路:创建生成任务 → analyze / write_article 行落库 → 管理端重新生成配图 → refigure 行 → RAG 问答 → rag_chat 行 → 文章发布 → embedding 行;看板页各卡片与表格出数
