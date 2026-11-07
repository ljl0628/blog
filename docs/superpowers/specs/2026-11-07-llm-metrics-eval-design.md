# LLM 度量闭环与质量评测 设计文档

日期:2026-11-07
状态:已确认(方案二:度量地基 + RAG 评测 + 生成时打分;不做 CI 集成与自动重写)

## 背景与目标

平台已具备完整的生成→审核→发布→问答链路,但缺两块:

1. **度量**:所有 LLM 调用的 token/耗时/成本/失败率没有落库,成本与性能不可见,优化无据可依
2. **质量评测**:RAG 问答与文章生成质量只能人工感受,改切块参数/prompt 前后无法量化对比

本设计补齐「度量 → 评测 → 优化 → 回归验证」闭环。叙事定位:每个简历亮点都要有
真实数据支撑(例:评测驱动修复围栏奇数块 bug 的案例已有,本设计将其体系化)。

## 关键决策

| 决策点 | 选择 | 理由 |
|---|---|---|
| 范围 | RAG 问答(客观指标)+ 文章生成(judge 打分)全覆盖 | 用户选定;生成侧只打分不重写 |
| 文章生成评测形态 | 生成时自动打分挂文章,不自动重写 | 质量门禁叙事;为 reflection 循环留干净钩子;本阶段「度量不干预生成」 |
| scene 传递 | ChatJSON/StreamChat 签名加显式 `scene string` 参数 | 显式可见,优于 ctx.WithValue 隐式;调用点约 5-8 处,改动可控 |
| 埋点写库 | 同步单行 insert,失败仅 log | 遵循「保持简单」;不上异步批量 |
| 成本计算 | 表只存 token,单价进 config,看板实时算 | 价目变动不洗历史数据 |
| 评测执行 | 后台 goroutine(带 ctx)+ run 表状态 + admin 轮询 | 20-50 条 1-3 分钟,同步 HTTP 会超时;不为此引 MQ |
| 评测集素材 | ask_unanswered 一键转入 | 真实用户无命中问题 → 评测集 → 调参 → 回归验证,完整闭环故事 |
| judge 模型 | `llm.judge_model` 留空复用主模型 | 与 `rag.chat_model` 同模式 |
| 看板图表 | Antd Statistic + 表格起步,不引图表库 | 轻起步;@ant-design/charts 需要时再加 |
| article 冗余字段 | `review_score`(最新总分,null=未评) | 实际查询字段非预留;避免「取最新一条 review」join |

## 总览:三个子模块

```
① 度量地基    llm.Client 埋点 ──→ llm_usage 表 ──→ admin「数据看板」
② RAG 评测    rag_eval_case(含 ask_unanswered 一键转入)
                   └→ 一键评测 ──→ rag_eval_run(命中率/引用正确率/judge 分)──→ 历史对比
③ 生成打分    pipeline 写完每篇 ──→ judge 评分 ──→ article_review + article 冗余分
                   └→ admin 审核列表看分(AI 分辅助人审)
```

## 数据模型

四张新表 + article 一个实用字段。无预留字段、无软删除,遵现有 model 惯例。

### llm_usage(度量明细)

| 字段 | 类型 | 说明 |
|---|---|---|
| id | uint PK | |
| scene | string(size:32, index 复合 created_at) | analyze / write_article / rag_chat / embedding / judge / refigure |
| model | string(size:64) | 实际调用模型 |
| prompt_tokens / completion_tokens | int | |
| duration_ms | int | |
| status | string(size:16) | success / error |
| error | string(size:512) | 失败摘要 |
| created_at | time | |

### rag_eval_case(评测集)

| 字段 | 类型 | 说明 |
|---|---|---|
| id | uint PK | |
| question | string(size:512) | |
| expect_article_ids | JSON | 期望命中的文章 ID 集合 |
| source | string(size:16) | manual / unanswered |
| enabled | bool | 停用不参与评测 |

### rag_eval_run(评测运行,保留全部历史)

| 字段 | 类型 | 说明 |
|---|---|---|
| id | uint PK | |
| status | string(size:16) | running / done / failed |
| total / hit_count | int | |
| hit_rate / citation_ok_rate | float | 客观指标 |
| avg_judge_score | float | judge 分(1-5,可空) |
| detail | JSON | 逐条:case id、命中与否、实际 top-k、citations、judge 分、错误 |
| error | string | run 级失败原因 |
| created_at | time | |

### article_review(文章评审,多次留痕)

| 字段 | 类型 | 说明 |
|---|---|---|
| id | uint PK | |
| article_id | uint(index) | |
| score_total | float | 总分 1-5 |
| score_accuracy / score_structure / score_readability / score_figure | float | 四维 |
| verdict | string(size:16) | pass / needs_review |
| judge_model | string(size:64) | |
| comment | string(size:512) | 一句中文评语 |
| created_at | time | |

### article 新增字段

- `review_score *float`(index):最新总分,落 review 时同步覆盖;null = 未评

## 埋点设计(internal/llm)

1. **前置**:补齐 `chatResponse` 的 usage 解析——OpenAI 兼容
   `usage.prompt_tokens/completion_tokens`,Anthropic `usage.input_tokens/output_tokens`;
   embedding 响应同理
2. **点位**:`chatWithRetry` 成功/失败返回处统一记录(覆盖 ChatJSON 全部调用方);
   `StreamChat` 完成处单独记;embedding 在 `Embed` 调用处记
3. **scene**:调用方显式传参;调用点:analyzeRepo(analyze)、writeArticle(write_article)、
   rag 问答(rag_chat)、embedding、judge(新)、管理端重新生成配图 prompt(refigure)
4. **写库**:同步 insert 到 llm_usage,失败 `log.Warn` 不影响主流程
5. Client 需新增 `*gorm.DB` 依赖(可为 nil,跳过埋点,与 rdb 限流同模式)

## admin 数据看板

新页面「数据」(仅统计聚合,不做原始明细翻页):

- Statistic 卡片:今日/本月成本(按 config 单价实时算)、总 token、调用次数、平均耗时、失败率
- 按日 × 场景聚合表格(日期、场景、调用数、token、耗时、成本)
- 查询用 SQL 聚合(service 层),不给前台暴露任何接口(admin JWT 路由组)

## RAG 评测流水线

### 评测集管理

- admin 新页「评测」:case 列表 + 增删改 + 启停
- `ask_unanswered` 列表加「转为评测条目」:question 带入,人工补 expect_article_ids,
  source=unanswered

### 执行

- 触发:`POST /api/v1/rag-eval/run`(admin JWT);当前仅允许一个 running(重复触发返回 409)
- 后台 goroutine 挂 ctx(服务停机可打断,中断的 run 标 failed)
- 每条 case 流程:问题 embedding → Qdrant search(top_k 与线上一致,同样阈值)
  → 记录命中(期望文章 ∈ top-k)→ 若 `rag.eval_judge`:完整问答跑一遍 →
  judge 对回答打分 + citations 正确性(引用命中期望文章之一)
- 单条失败记入 detail(status=error),不中断整轮
- run 完成写汇总指标;历史 run 列表页展示各轮指标,天然形成「改参前后对比」

### 指标定义

- hit_rate = 命中 case 数 / 总 case 数(命中 = expect_article_ids 与 top-k 文章集合有交集)
- citation_ok_rate = citations 与期望文章集有交集的 case 占比(引用了期望文章之一即通过,
  与 hit_rate 同口径对称);回答无 citations 视为不通过;夹带无关/幻觉引用不因此判负(2026-11-07 放宽,
  此前为子集口径——历史 run 不回算,跨口径对比注意断点)
- avg_judge_score = 开启 judge 时回答质量均分(1-5);检索无结果的 case 跳过打分记 null
  不参与均分(其回答为固定话术,质量已由 hit_rate 度量)

## 生成时 judge 打分

- 挂载点:pipeline `saveFiguresAndCover` 之后、写作循环内(pipeline.go ④ 段);
  进度不打新步骤,`prog.logf` 带一句「质量分 x.x/5」
- prompt:输入文章 markdown + summary + 配图 spec;输出 JSON:
  `score_accuracy/structure/readability/figure + score_total + verdict + comment`;
  走现有 `ChatJSON`(scene=judge,validate 仿 writeArticle 校验分数区间)
- **失败策略:judge 失败不阻塞生成**——log 警告、文章无分(与 SVG 渲染失败跳过同哲学)
- 幂等:文章重生成 → 追加新 review 行(留痕)+ 覆盖 article.review_score
- `llm.judge_enabled=false` 时整段跳过

## 配置新增(三处同步:config.go + config.yaml + .env.example)

```yaml
llm:
  judge_model: ""      # 留空复用主模型
  judge_enabled: true  # 生成时自动打分
  pricing:             # 每 1k token 单价(元),看板算成本;数值为示例,实施时按厂商最新价目填
    glm-5.3: { prompt: 0.5, completion: 2.0 }
rag:
  eval_judge: true     # 评测轮是否跑 judge 评回答
```

## 错误处理与幂等

- 埋点失败:log 不阻塞主流程
- judge 失败:文章无分,pipeline 继续
- 评测单条失败:记 detail 不中断;run 级失败标 failed + error
- 评测重跑:每次 run 新行,无重投场景,不涉 MQ 幂等
- 全部阻塞操作带 ctx(评测 goroutine、LLM 调用、DB)

## 测试(service 层单测,遵现有惯例)

- hit_rate / citation_ok_rate 计算(纯函数)
- judge 输出 JSON 解析与 validate 校验
- usage 解析(OpenAI / Anthropic 两格式)
- 评测 detail 汇总统计

## 明确不做的(防蔓延)

- CI / 定时评测回归(方案三内容,落地后有余力再加)
- 不合格自动重写(reflection 循环,后续演进钩子)
- 趋势折线图、prompt 版本管理、A/B
- llm_usage 原始明细翻页页(聚合够用)

## 实施节奏(2-4 周)

| 周 | 内容 | 产出 |
|---|---|---|
| 1 | usage 解析 + 埋点 + llm_usage 表 + 看板骨架 | 数据开始积累 |
| 2 | 评测集管理 + 执行 + 报告页 | 第一轮基线数据 |
| 3 | 生成打分 + admin 展示 + 打磨 | 全功能闭环 |
| 4(缓冲) | 真实评测、调参拿对比数据、截图 | 简历素材成型 |
