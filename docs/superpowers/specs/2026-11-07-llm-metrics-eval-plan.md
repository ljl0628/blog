# LLM 度量闭环与质量评测 实施计划 · 第一阶段(度量地基)

日期:2026-11-07
对应设计:[2026-11-07-llm-metrics-eval-design.md](2026-11-07-llm-metrics-eval-design.md)
范围:**仅第一阶段**(usage 解析 + 埋点 + llm_usage 表 + admin 看板骨架)。
评测流水线、生成时 judge 打分为第二/三阶段,另行出 plan。

规范依据:
- `.claude/specs/common/project-constraints.spec.md`(配置三处同步、ctx 必带、中文注释、提交验证闭环)
- `.claude/specs/coding/go-backend.spec.md`(分层:handler 薄且禁持 db;枚举值域类型化;错误 `%w` 包装;service 入参展开)
- `.claude/specs/coding/web-frontend.spec.md`(请求走 `api/http.ts`;类型对齐 `types/`;函数组件)

## 关键实现决策(设计文档的落地细化)

| 决策 | 结论 | 理由 |
|---|---|---|
| scene 类型 | `model.LlmScene`(`type LlmScene string` + 常量),llm 包引用 model | go-backend 枚举值域规范;与 system/user 参数不同型,换位编译不过 |
| 埋点粒度 | `chatWithRetry` 层(物理请求粒度,一次 HTTP 往返一行) | 重试/校验重试的真实成本可见;JSON 校验重试会产生新行,符合"记真实消耗" |
| usage 传递 | `chatOnce` / `chatWithRetry` 返回值追加 `usageInfo{Prompt, Completion int}` | 改动收敛在 llm 包内部 |
| 流式 token | OpenAI 流式加 `stream_options.include_usage`;Anthropic 流式解析 `message_start.usage.input_tokens` 与 `message_delta.usage.output_tokens`;解析不到记 0 | 流式调用次数/耗时/状态全覆盖,token 尽力而为 |
| 埋点写库 | Client/Embedder 各加 `db *gorm.DB`(nil 跳过,与 rdb 限流同模式),同步 insert,失败仅 `log.Warn` | 设计已确认;保持简单 |
| pricing 仅走 yaml | `.env.example` 不加(env 无法覆盖 map 结构,非密钥无此需求) | 三处同步红线针对可 env 覆盖项;此处取舍在 plan 明示 |
| 看板数据获取 | 前端组件内直接 `http.get` + useState | 遵 RagIndex/AskUnanswered 只读页惯例,不加 store |

## 文件变更清单

### 后端(按分层 router → api → service → repo/model/config → llm)

| 文件 | 类型 | 内容 |
|---|---|---|
| `internal/model/llm_usage.go` | 新增 | `LlmUsage` 表模型 + `LlmScene` 类型与常量(analyze/write_article/rag_chat/embedding/judge/refigure) |
| `internal/repo/db.go` | 修改 | `Migrate` 注册 `&model.LlmUsage{}` |
| `internal/config/config.go` | 修改 | `LLM` 结构加 `Pricing map[string]LLMPrice`(`Prompt`/`Completion` float64,元/1k token) |
| `server/config.yaml` | 修改 | `llm.pricing` 示例价目(注释标明按厂商最新价目调) |
| `internal/llm/types.go` | 修改 | `chatResponse` 加 `Usage{prompt_tokens,completion_tokens}`;`anthropicResponse` 加 `Usage{input_tokens,output_tokens}`;`embedResponse` 加 `Usage{prompt_tokens,total_tokens}`;新增 `usageInfo` |
| `internal/llm/client.go` | 修改 | `Client` 加 `db *gorm.DB`;`NewClient` 加参;`ChatJSON`/`chatWithRetry` 签名加 scene 与 usageInfo 返回;`recordUsage`(组装 LlmUsage 落库,失败 Warn) |
| `internal/llm/client_anthropic.go` | 修改 | `chatOnce` 分发与 `chatOnceAnthropic` 返回 usageInfo |
| `internal/llm/stream.go` | 修改 | `StreamChat` 加 scene 参数;`streamOpenAI` 加 `stream_options.include_usage` 并解析 usage 帧;**顺手更新 :14 过时注释**(「流式不计入用量」→ 本功能落地后计入了) |
| `internal/llm/stream_anthropic.go` | 修改 | 事件解析补 `message_start.usage.input_tokens` / `message_delta.usage.output_tokens` 累积 |
| `internal/llm/embedding.go` | 修改 | `Embedder` 加 `db`;`NewEmbedder` 加参;`Embed` 成功/失败处记录(scene=embedding,completion=0,prompt=usage.prompt_tokens) |
| `internal/task/pipeline.go` | 修改 | 两处 `ChatJSON` 调用传 scene(analyze / write_article) |
| `internal/service/article.go` | 修改 | :244 `ChatJSON` 传 scene(refigure) |
| `internal/service/rag.go` | 修改 | :152 ChatJSON 与 :315 StreamChat 传 scene(rag_chat) |
| `internal/app/bootstrap_pipeline.go` | 修改 | `llm.NewClient` / `NewEmbedder` 调用补 db 实参(两处 client 都埋) |
| `internal/service/metrics.go` | 新增 | `MetricsService`:`Summary(ctx)`(今日/本月成本、总 token、调用数、平均耗时、失败率,成本按 cfg.Pricing 查表)+ `Daily(ctx, days)`(按日×场景 GROUP BY 聚合) |
| `internal/api/metrics_handler.go` | 新增 | 薄 handler:参数绑定(days)→ 调 service → resp 封装 |
| `internal/router/router.go` | 修改 | Deps 加 `Metrics`;admin 组注册 `GET /metrics/summary`、`GET /metrics/daily` |

### 前端 admin

| 文件 | 类型 | 内容 |
|---|---|---|
| `web/admin/src/types/metrics.ts` | 新增 | `MetricsSummary` / `MetricsDailyRow`(与后端 resp 对齐) |
| `web/admin/src/pages/Metrics.tsx` | 新增 | Statistic 卡片行(今日成本/本月成本/总 token/调用数/平均耗时/失败率)+ 按日×场景 Table;组件内 `http.get` |
| `web/admin/src/router/config.tsx` | 修改 | APP_ROUTES 加 `{ path: '/metrics', title: '数据', icon: BarChartOutlined }`(带 title 自动进菜单) |

### 测试

| 文件 | 类型 | 内容 |
|---|---|---|
| `internal/llm/usage_test.go` | 新增 | OpenAI / Anthropic / embedding 三格式 usage JSON 解析 |
| `internal/service/metrics_test.go` | 新增 | 成本查表计算纯函数(含模型不在价目表时返回 0) |

## 接口与数据契约

### 路由(均 admin JWT 组)

```
GET /api/v1/metrics/summary          → { today_cost, month_cost, total_tokens,
                                         call_count, avg_duration_ms, error_rate }
GET /api/v1/metrics/daily?days=30    → [{ date, scene, calls, prompt_tokens,
                                         completion_tokens, avg_duration_ms, cost }]
```

### llm_usage 表(AutoMigrate 自动建)

scene 用 `model.LlmScene` 类型化落库;`idx_scene_created` 复合索引(scene, created_at)支撑看板聚合。纯 append 日志型数据,无软删、无更新,不涉幂等。

## 复用清单

- `llm.Client` / `Embedder` / `chatWithRetry` 重试骨架:埋点内嵌,不改调用语义
- `resp/` 响应封装、`middleware.JWTAuth`:handler 与路由照现有模式
- `app/` 手工装配 + `router.Deps` 注入:MetricsService 同 pattern
- 前端 `api/http.ts`、Antd Statistic/Table:零新依赖

## 配置变更(三处同步说明)

- `config.go`:`LLM.Pricing map[string]LLMPrice`,LLMPrice{Prompt, Completion float64}
- `config.yaml`:`llm.pricing` 按模型列单价(示例值,注释标注实施时按厂商最新价目核对)
- `.env.example`:**不加**(pricing 非密钥、map 结构无法用环境变量覆盖;此为对"三处同步"的有意例外,已在决策表明示)

## 缓存与一致性影响

- 不触及 `/portal/*` 与前台 Redis 缓存;无 Qdrant / MQ 变更
- 埋点 insert 失败仅 Warn,不影响任何主流程(生成/问答/配图)
- AutoMigrate 新增表为纯增量,存量表零改动

## 实施步骤(按序)

1. `model/llm_usage.go`(含 LlmScene)+ `repo/db.go` 注册 + `config`(Pricing 三处)
2. `llm/types.go` usage 字段 → `client.go` / `client_anthropic.go` usageInfo 链路 + `recordUsage`
3. 调用点签名改造:pipeline.go / article.go / rag.go 传 scene;`app/` 装配补 db
4. stream.go / stream_anthropic.go 流式 usage + 注释更新;embedding.go 埋点
5. `service/metrics.go` 聚合 + `api/metrics_handler.go` + 路由注册
6. 前端 types + Metrics.tsx + 菜单
7. 单测(usage 解析 / 成本计算)
8. 验证闭环:`task vet` + `task build` + `go test ./...` + admin `npm run build`;
   本地跑一次生成任务,确认 llm_usage 有行、看板页出数

## 本阶段不做(防蔓延)

- RAG 评测集 / 评测执行(第二阶段)
- judge 打分与 `llm.judge_model` / `judge_enabled` 配置(第三阶段,本阶段不预留)
- 图表库引入、usage 明细翻页、成本告警
