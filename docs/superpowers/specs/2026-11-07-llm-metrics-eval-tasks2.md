# LLM 度量闭环 · 第二/三阶段任务清单(RAG 评测 + 生成打分)

对应:[design](2026-11-07-llm-metrics-eval-design.md) · 第一阶段已上线(llm_usage 埋点 + 看板)
本文档合并第二(RAG 评测流水线)与第三(生成时 judge 打分)阶段,一次性实施。

## Wave A(model / config 层,可并行)

- [x] **任务 A1:评测与评审数据模型**
  | 文件: `model/rag_eval.go`(新增:RagEvalCase、RagEvalRun)、`model/article_review.go`(新增:ArticleReview)、`model/article.go`(加 ReviewScore *float + 索引)、`repo/db.go`(Migrate 注册三表)
  | 验收: build 过;三表自动创建;article 加列 review_score;无预留无软删

- [x] **任务 A2:judge 与评测配置**
  | 文件: `config/config.go`、`config.yaml`、`.env.example`(bool/字符串可 env 覆盖,三处同步)
  | 内容: `llm.judge_model`(空=复用主模型)、`llm.judge_enabled`(默认 true)、`rag.eval_judge`(默认 true)
  | 验收: build 过;三处同步;默认值语义正确

## Wave B(service 层,依赖 A)

- [x] **任务 B1:RAG 评测 service(集合 + 执行)**
  | 文件: `service/rag_eval.go`(新增)
  | 依赖: A1、A2
  | 内容: RagEvalService——评测集 CRUD(ListCases/CreateCase/UpdateCase/DeleteCase);`ImportFromUnanswered(ctx, id, expectIDs)`;`RunEval(ctx)`(单 running 锁:已有 running 返回错误;后台 goroutine 带 ctx,停机可打断,中断标 failed);每 case:Embed 问题 → Search(top_k/score_threshold 与线上一致)→ 命中判定(expect_article_ids ∩ top-k 文章集非空)→ eval_judge 开启时完整问答 + judge 打分 + citations 正确性(citations 只含期望文章,无 citations 视为不通过)→ 逐条 detail JSON;完成写汇总(total/hit_count/hit_rate/citation_ok_rate/avg_judge_score);单条失败记 detail 不中断
  | 验收: build + vet 过;`go test ./internal/service/` 过;命中/引用判定抽纯函数并单测

- [x] **任务 B2:生成时 judge 打分(pipeline 挂载)**
  | 文件: `llm/prompt.go`(加 BuildJudgePrompt)、`task/pipeline.go`(saveFiguresAndCover 后挂 judgeJudgeArticle)、`task/` 或 `service/` 内落库逻辑
  | 依赖: A1、A2
  | 内容: judge prompt 输入文章 markdown+summary+figures spec,输出 JSON(score_accuracy/structure/readability/figure 各 1-5、score_total、verdict pass|needs_review、comment ≤200 字);走 ChatJSON(scene=judge)validate 校验分数区间;失败 log Warn 跳过不阻塞生成(judge_enabled=false 整段跳过);成功落 article_review + 覆盖 article.review_score;文章重生成追加新 review 行;prog.logf 带「质量分 x.x/5」;judge_model 空用主模型
  | 验收: build/vet/test 过;不改变 pipeline 失败语义(judge 挂了任务照常 success)

## Wave C(api + 前端,依赖 B)

- [x] **任务 C1:评测与打分接口**
  | 文件: `api/rag_eval_handler.go`(新增)、`router/router.go`
  | 依赖: B1
  | 路由(admin JWT): `GET/POST /rag-eval/cases`、`PUT/DELETE /rag-eval/cases/:id`、`POST /rag-eval/cases/import-unanswered`、`POST /rag-eval/run`(409 单 running)、`GET /rag-eval/runs`(历史,含最新状态轮询)
  | 验收: build 过;handler 薄

- [x] **任务 C2:admin 前端(评测页 + 文章质量分)**
  | 文件: `types/ragEval.ts`、`pages/RagEval.tsx`(新增);`pages/Articles.tsx`、`pages/ArticleEdit.tsx`、`types/article.ts`(质量分列/评分卡)
  | 依赖: C1(契约先定可并行,联调依赖 C1)
  | 内容: 评测页=case 表格(增删改启停)+从无命中问题导入弹窗+「运行评测」按钮(轮询 run 状态)+历史 run 列表(hit_rate/citation_ok_rate/avg_judge_score 对比);文章列表加「质量分」列(tag 颜色分档:≥4 绿 / 3-4 橙 / <3 红 / 未评灰,tooltip 四维+评语);编辑页顶部评分卡
  | 验收: `npm run build` 过;无 any

## Wave D(端到端,主线自行执行)

- [x] **任务 D1:端到端验证**
  | 验收: 全量 vet/build/test + 双前端 build;造 5-8 条真实评测 case(基于已发布文章)跑通一轮评测出基线指标;触发一次文章生成看 judge 打分落库;看板/评测页数据一致
