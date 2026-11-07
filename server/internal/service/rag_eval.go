package service

// RAG 回归评测：评测集（问题 + 期望命中文章）维护与评测轮执行。
// 评测逐条重放与线上问答完全同口径的检索链（RagService.retrieve），
// eval_judge 开启时再跑完整问答（RagService.ask）并用 LLM judge 给回答打分，
// 汇总命中率 / 引用正确率 / 平均 judge 分——调整 top_k / score_threshold / rerank
// 前后各跑一轮，指标对比即检索质量的回归基线。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"blog/server/internal/config"
	"blog/server/internal/model"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	// evalCaseTimeout 单条用例的评测预算：embedding + 检索，judge 开启时再叠加
	// 完整流式问答与 judge 打分两次 LLM 调用，给足上限防单条卡死拖住整轮
	//（与 rag_admin.go 单篇重建超时同一哲学）。
	evalCaseTimeout = 3 * time.Minute

	// evalRunWriteTimeout 评测轮结果写库超时：用独立 ctx——中断路径下评测 ctx
	// 已取消，不能带着取消的 ctx 写最终结果（同 CaptureUnanswered 的兜底模式）。
	evalRunWriteTimeout = 5 * time.Second

	// evalRunStaleSlack 判定 running 残留的额外宽限：单条预算 × 用例数之上
	// 再留一段调度/网络抖动余量，避免误判仍在跑的轮。
	evalRunStaleSlack = 10 * time.Minute
)

// RagEvalService RAG 回归评测：评测集 CRUD 与评测轮执行。
// rag 与线上问答同实例（同包直接复用其私有检索链 retrieve / 问答 ask 与 chat
// 客户端，judge 打分走同一 client）；cfg 与 rag 同源（装配层传同一份 cfg.RAG），
// 保证评测阈值等参数与线上完全一致。
type RagEvalService struct {
	db  *gorm.DB
	rag *RagService
	cfg config.RAG
	log *zap.Logger

	mu     sync.Mutex         // 串行化「单 running 检查 + 建 run」与 cancel 字段读写
	cancel context.CancelFunc // 当前评测轮 ctx 的取消函数；nil = 无进行中的轮
}

// NewRagEvalService 创建评测服务。
func NewRagEvalService(db *gorm.DB, rag *RagService, cfg config.RAG, log *zap.Logger) *RagEvalService {
	return &RagEvalService{db: db, rag: rag, cfg: cfg, log: log}
}

// ---------------- 评测集 CRUD ----------------

// ListCases 全量评测用例（按 id 升序，含停用项）。用例量为个位数到十位数，不分页。
func (s *RagEvalService) ListCases(ctx context.Context) ([]model.RagEvalCase, error) {
	cases := make([]model.RagEvalCase, 0)
	if err := s.db.WithContext(ctx).Order("id ASC").Find(&cases).Error; err != nil {
		return nil, fmt.Errorf("查询评测用例: %w", err)
	}
	return cases, nil
}

// CreateCase 新增人工评测用例（source=manual，默认启用）。
func (s *RagEvalService) CreateCase(ctx context.Context, question string, expectIDs []uint) (*model.RagEvalCase, error) {
	return s.createCase(ctx, question, expectIDs, model.RagEvalSourceManual)
}

// UpdateCase 更新用例的问题 / 期望文章集 / 启停状态。
func (s *RagEvalService) UpdateCase(ctx context.Context, id uint, question string, expectIDs []uint, enabled bool) (*model.RagEvalCase, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, fmt.Errorf("%w: 问题不能为空", ErrInvalid)
	}
	ids, err := s.validateArticleIDs(ctx, expectIDs)
	if err != nil {
		return nil, err
	}
	var c model.RagEvalCase
	if err := s.db.WithContext(ctx).First(&c, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: 评测用例不存在", ErrNotFound)
		}
		return nil, fmt.Errorf("查询评测用例: %w", err)
	}
	c.Question = truncateRunes(question, 512)
	c.ExpectArticleIDs = jsonify(ids)
	c.Enabled = enabled
	if err := s.db.WithContext(ctx).Save(&c).Error; err != nil {
		return nil, fmt.Errorf("更新评测用例: %w", err)
	}
	return &c, nil
}

// DeleteCase 删除用例（正在跑的评测轮持有触发时刻的快照，不受影响）。
func (s *RagEvalService) DeleteCase(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&model.RagEvalCase{}, id)
	if res.Error != nil {
		return fmt.Errorf("删除评测用例: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: 评测用例不存在", ErrNotFound)
	}
	return nil
}

// ImportFromUnanswered 把无命中问题转为评测用例（source=unanswered）：
// 问题原文带入，期望文章集由人工指定（选题回流的补录路径）。
func (s *RagEvalService) ImportFromUnanswered(ctx context.Context, unansweredID uint, expectIDs []uint) (*model.RagEvalCase, error) {
	var ua model.AskUnanswered
	if err := s.db.WithContext(ctx).First(&ua, unansweredID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: 无命中问题不存在", ErrNotFound)
		}
		return nil, fmt.Errorf("查询无命中问题: %w", err)
	}
	return s.createCase(ctx, ua.Question, expectIDs, model.RagEvalSourceUnanswered)
}

// createCase 建用例共用实现：问题非空、期望文章集非空且全部存在。
func (s *RagEvalService) createCase(ctx context.Context, question string, expectIDs []uint, source model.RagEvalSource) (*model.RagEvalCase, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, fmt.Errorf("%w: 问题不能为空", ErrInvalid)
	}
	ids, err := s.validateArticleIDs(ctx, expectIDs)
	if err != nil {
		return nil, err
	}
	c := &model.RagEvalCase{
		Question:         truncateRunes(question, 512),
		ExpectArticleIDs: jsonify(ids),
		Source:           source,
		Enabled:          true,
	}
	if err := s.db.WithContext(ctx).Create(c).Error; err != nil {
		return nil, fmt.Errorf("创建评测用例: %w", err)
	}
	return c, nil
}

// validateArticleIDs 校验期望文章集：非空、无 0 值、去重后全部存在于 article 表；
// 返回去重结果。提前校验让「填了不存在/已删除文章」在建用例时即暴露，
// 而不是每轮评测都静默 miss。
func (s *RagEvalService) validateArticleIDs(ctx context.Context, ids []uint) ([]uint, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: 期望命中文章不能为空", ErrInvalid)
	}
	seen := make(map[uint]struct{}, len(ids))
	uniq := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			return nil, fmt.Errorf("%w: 文章 ID 不能为 0", ErrInvalid)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	var n int64
	if err := s.db.WithContext(ctx).Model(&model.Article{}).Where("id IN ?", uniq).Count(&n).Error; err != nil {
		return nil, fmt.Errorf("校验期望文章: %w", err)
	}
	if int(n) != len(uniq) {
		return nil, fmt.Errorf("%w: 期望文章存在无效 ID（%d/%d 个存在）", ErrInvalid, n, len(uniq))
	}
	return uniq, nil
}

// ---------------- 评测轮执行 ----------------

// RunEval 触发一轮评测：单 running 锁校验 + 用例集快照后建 run 行（running），
// 实际执行放后台 goroutine（脱离请求生命周期，HTTP 返回后继续跑），
// 立即返回 run 供前端轮询。已有进行中的轮返回 ErrConflict（handler 映射 409）。
func (s *RagEvalService) RunEval(ctx context.Context) (*model.RagEvalRun, error) {
	if !s.rag.Enabled() {
		return nil, fmt.Errorf("问答功能未启用")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// 单 running 锁（进程内锁防并发触发竞态；DB 状态查询看历史与残留）
	var running model.RagEvalRun
	err := s.db.WithContext(ctx).Where("status = ?", model.RagEvalRunStatusRunning).First(&running).Error
	if err == nil {
		// running 超过「用例数 × 单条预算 + 宽限」即进程崩溃残留（正常轮不可能超过），
		// 标 failed 放行本轮，避免一次崩溃把评测入口永久卡死在 409
		if time.Since(running.CreatedAt) <= time.Duration(running.Total)*evalCaseTimeout+evalRunStaleSlack {
			return nil, fmt.Errorf("%w: 已有评测轮在进行中（run %d）", ErrConflict, running.ID)
		}
		s.log.Warn("发现超过最长时长的 running 评测轮，判定为进程中断残留并标记失败",
			zap.Uint("run_id", running.ID), zap.Time("created_at", running.CreatedAt))
		if err := s.db.WithContext(ctx).Model(&model.RagEvalRun{}).Where("id = ?", running.ID).
			Updates(map[string]any{
				"status": model.RagEvalRunStatusFailed,
				"error":  "评测进程中断，run 残留（新一轮触发时自动标记）",
			}).Error; err != nil {
			return nil, fmt.Errorf("清理残留评测轮: %w", err)
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("查询进行中的评测轮: %w", err)
	}

	// 用例集快照：本轮评测针对触发时刻的启用集，跑动中的增删改不影响本轮结果
	var cases []model.RagEvalCase
	if err := s.db.WithContext(ctx).Where("enabled = ?", true).Order("id ASC").Find(&cases).Error; err != nil {
		return nil, fmt.Errorf("查询评测用例: %w", err)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("%w: 没有启用中的评测用例", ErrInvalid)
	}
	run := model.RagEvalRun{Status: model.RagEvalRunStatusRunning, Total: len(cases)}
	if err := s.db.WithContext(ctx).Create(&run).Error; err != nil {
		return nil, fmt.Errorf("创建评测轮: %w", err)
	}

	// 独立 ctx：不随请求结束而取消；cancel 存字段供 Stop 打断（goroutine 可退出，禁止裸悬挂）
	evalCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.execute(evalCtx, run.ID, cases)
	return &run, nil
}

// Stop 打断进行中的评测轮（优雅停机时由装配层调用）；无进行中的轮时空操作。
// 被打断的轮由 execute 落为 failed。
func (s *RagEvalService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// ragEvalDetail run.Detail JSON 数组的单条元素：逐用例的判定与执行结果。
type ragEvalDetail struct {
	CaseID        uint     `json:"case_id"`
	Question      string   `json:"question"`
	Hit           bool     `json:"hit"`            // 检索命中：期望文章 ∈ top-k 文章集
	ExpectIDs     []uint   `json:"expect_ids"`     // 期望命中的文章 ID
	ActualIDs     []uint   `json:"actual_ids"`     // 实际检索 top-k 文章 ID（按得分降序去重）
	CitationOK    bool     `json:"citation_ok"`    // 引用正确：正文标注的引用非空且命中期望文章之一（交集口径，与命中判定对称）
	JudgeScore    *float64 `json:"judge_score"`    // LLM judge 分（1-5）；未开启/检索无结果/失败为 null
	AnswerExcerpt string   `json:"answer_excerpt"` // 回答摘要（截 200 字）
	Error         string   `json:"error"`          // 单条执行错误摘要；空 = 正常
}

// execute 评测轮主体（后台 goroutine）：逐条评测 → 汇总指标 → 更新 run 行。
// ctx 取消（Stop/停机）时立即中断，剩余用例放弃，run 标 failed 并保留已跑明细。
func (s *RagEvalService) execute(ctx context.Context, runID uint, cases []model.RagEvalCase) {
	// 轮结束清理 cancel 字段（RunEval 的锁保证同一时刻仅本轮持有）
	defer func() {
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()

	details := make([]ragEvalDetail, 0, len(cases))
	hitCount, citationOKCount, judgeCount := 0, 0, 0
	var judgeSum float64
	for i := range cases {
		if ctx.Err() != nil { // Stop/停机打断，剩余用例放弃
			break
		}
		cctx, cancel := context.WithTimeout(ctx, evalCaseTimeout)
		d := s.evaluateCase(cctx, cases[i])
		cancel()
		if d.Hit {
			hitCount++
		}
		if d.CitationOK {
			citationOKCount++
		}
		if d.JudgeScore != nil {
			judgeSum += *d.JudgeScore
			judgeCount++
		}
		if d.Error != "" {
			s.log.Warn("评测用例执行失败", zap.Uint("case_id", d.CaseID), zap.String("error", d.Error))
		}
		details = append(details, d)
	}

	total := len(cases)
	run := model.RagEvalRun{
		Status:         model.RagEvalRunStatusDone,
		Total:          total,
		HitCount:       hitCount,
		HitRate:        float64(hitCount) / float64(total),
		CitationOkRate: float64(citationOKCount) / float64(total),
		Detail:         jsonify(details),
	}
	if judgeCount > 0 { // judge 关闭或全部打分失败时保持 NULL
		avg := judgeSum / float64(judgeCount)
		run.AvgJudgeScore = &avg
	}
	if ctx.Err() != nil && len(details) < total { // 全部跑完才收到取消不算中断
		run.Status = model.RagEvalRunStatusFailed
		run.Error = "评测被中断（服务停机或手动停止），仅完成部分用例"
	}
	// 显式 Select 强制写零值列（如 hit_rate=0），struct Updates 默认会跳过零值
	wctx, wcancel := context.WithTimeout(context.Background(), evalRunWriteTimeout)
	defer wcancel()
	if err := s.db.WithContext(wctx).Model(&model.RagEvalRun{}).Where("id = ?", runID).
		Select("status", "total", "hit_count", "hit_rate", "citation_ok_rate", "avg_judge_score", "detail", "error").
		Updates(run).Error; err != nil {
		s.log.Error("更新评测轮结果失败", zap.Uint("run_id", runID), zap.Error(err))
		return
	}
	s.log.Info("评测轮结束",
		zap.Uint("run_id", runID),
		zap.String("status", string(run.Status)),
		zap.Int("total", total),
		zap.Int("hit", hitCount),
		zap.Int("citation_ok", citationOKCount))
}

// evaluateCase 评测单条用例（ctx 已含单条超时预算）：
// 检索链重放 → 命中判定 →（judge 开启时）完整问答 → 引用判定 → judge 打分。
// 任一步失败记入 detail.Error 返回，不外抛——单条失败不中断整轮。
func (s *RagEvalService) evaluateCase(ctx context.Context, c model.RagEvalCase) ragEvalDetail {
	d := ragEvalDetail{CaseID: c.ID, Question: c.Question, ExpectIDs: []uint{}, ActualIDs: []uint{}}
	expect, err := parseCaseExpectIDs(c.ExpectArticleIDs)
	if err != nil {
		d.Error = fmt.Sprintf("期望文章集解析失败: %v", err)
		return d
	}
	d.ExpectIDs = expect

	// 检索链重放：与线上 Ask 完全同口径（embed → 阈值过滤 → rerank → top_k → 按文章分组）
	hits, _, err := s.rag.retrieve(ctx, c.Question)
	if err != nil {
		d.Error = fmt.Sprintf("检索失败: %v", err)
		return d
	}
	groups := groupByArticle(hits, s.cfg.ScoreThreshold)
	groupSlugs := make([]string, 0, len(groups))
	for _, g := range groups {
		groupSlugs = append(groupSlugs, g.slug)
	}
	idBySlug := make(map[string]uint, len(groupSlugs))
	groupRaw, err := s.articleIDsForSlugs(ctx, idBySlug, groupSlugs)
	if err != nil {
		d.Error = fmt.Sprintf("解析检索结果文章: %v", err)
		return d
	}
	for _, id := range groupRaw {
		if id != 0 { // 0 = slug 解析不到（向量残留/文章已删），不进 top-k 文章集
			d.ActualIDs = append(d.ActualIDs, id)
		}
	}
	d.Hit = evalHit(expect, d.ActualIDs)
	if !s.cfg.EvalJudge {
		return d
	}

	// 完整问答（与线上完全同链）：onDelta 累积回答文本供摘录与 judge；
	// captureUnanswered=false：评测重放不推高 ask_unanswered 的选题回流计数
	var answer strings.Builder
	_, err = s.rag.ask(ctx, c.Question, nil, func(delta string) { answer.WriteString(delta) }, false)
	if err != nil {
		d.Error = fmt.Sprintf("问答失败: %v", err)
		return d
	}
	d.AnswerExcerpt = truncateRunes(answer.String(), 200)

	// 引用判定基于模型正文实际标注的 [n]（上下文分组序号，见 rag.go ask 的组装约定），
	// 而非 ask 返回的 citations——那是进入上下文的全部资料来源（含未被标注引用的
	// 噪声文章），用它判定会把「检索混入噪声」误报成「引用错误」。序号越界 =
	// 幻觉引用，以 0 占位参与判定自然不通过。
	citedIDs := make([]uint, 0, 4)
	validSlugs := make([]string, 0, 4)
	for _, n := range parseAnswerCitationIdx(answer.String()) {
		if 1 <= n && n <= len(groups) {
			validSlugs = append(validSlugs, groups[n-1].slug)
		} else {
			citedIDs = append(citedIDs, 0)
		}
	}
	if ids, err := s.articleIDsForSlugs(ctx, idBySlug, validSlugs); err != nil {
		appendDetailErr(&d, fmt.Sprintf("解析引用文章: %v", err))
	} else {
		// 解析不到的 slug 以 0 占位：0 永不在合法期望集内，引用判定自然不通过
		citedIDs = append(citedIDs, ids...)
		d.CitationOK = evalCitationOK(expect, citedIDs)
	}

	// 检索无结果时回答是固定话术：judge 对照空参考打分测的是语料覆盖而非
	// 回答质量（已由命中率度量），跳过打分记 null 不参与均分。
	if len(d.ActualIDs) == 0 {
		return d
	}
	score, err := s.judgeAnswer(ctx, groups, c.Question, answer.String())
	if err != nil {
		appendDetailErr(&d, fmt.Sprintf("judge 打分失败: %v", err))
	} else {
		d.JudgeScore = &score
	}
	return d
}

// appendDetailErr 把后发生的错误摘要叠加进 detail.Error（同一条可多步失败，分号分隔）。
func appendDetailErr(d *ragEvalDetail, msg string) {
	if d.Error != "" {
		d.Error += "; "
	}
	d.Error += msg
}

// evalJudgeSystemPrompt 评测 judge 规则：基于参考片段评回答质量，1-5 分。
const evalJudgeSystemPrompt = `你是 RAG 问答质量的评审员。依据「参考文章片段」评估「回答」对「问题」的质量，打 1-5 分：
5 = 完全依据片段、准确且完整；4 = 基本正确，仅有小瑕疵；3 = 部分正确但缺漏明显；
2 = 多处与片段不符或内容空洞；1 = 编造内容、答非所问或未作回答。
只输出一个 JSON 对象：{"score": 1到5的整数, "reason": "100 字内的评分理由"}，不要输出任何其他内容。`

// judgeAnswer LLM judge 给单条回答打分（1-5），validate 校验分数区间。
// scene 复用 rag_chat 而非新增场景值：评测 judge 属 RAG 问答域，token 并入
// rag_chat 用量即可在看板观测，不动 model 层场景枚举。
// reason 只用于约束模型给出审慎分数，不落 detail（detail 结构按设计文档定死）。
func (s *RagEvalService) judgeAnswer(ctx context.Context, groups []*articleGroup, question, answer string) (float64, error) {
	var ctxParts strings.Builder
	for _, g := range groups {
		for _, ct := range g.contents {
			ctxParts.WriteString(fmt.Sprintf("《%s》 %s\n%s\n\n", g.title, ct.path, truncateRunes(ct.text, 500)))
		}
	}
	user := fmt.Sprintf("参考文章片段：\n%s\n用户问题：%s\n\n回答：\n%s",
		ctxParts.String(), question, answer)
	var out struct {
		Score  float64 `json:"score"`
		Reason string  `json:"reason"`
	}
	err := s.rag.chat.ChatJSON(ctx, model.LlmSceneRagChat, evalJudgeSystemPrompt, user, &out, func() error {
		if out.Score < 1 || out.Score > 5 {
			return fmt.Errorf("score 必须在 1-5 之间，得到 %v", out.Score)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return out.Score, nil
}

// ---------------- 判定纯函数（单测见 rag_eval_test.go） ----------------

// parseCaseExpectIDs 解析用例的期望文章 ID 集（model.JSON 存 uint 数组）。
func parseCaseExpectIDs(raw model.JSON) ([]uint, error) {
	if len(raw) == 0 {
		return nil, errors.New("期望文章集为空")
	}
	var ids []uint
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, fmt.Errorf("解析期望文章集: %w", err)
	}
	if len(ids) == 0 {
		return nil, errors.New("期望文章集为空")
	}
	return ids, nil
}

// evalHit 命中判定（纯函数）：期望文章集与实际 top-k 文章集存在交集即命中。
func evalHit(expectIDs, actualIDs []uint) bool {
	if len(expectIDs) == 0 || len(actualIDs) == 0 {
		return false
	}
	expect := make(map[uint]struct{}, len(expectIDs))
	for _, id := range expectIDs {
		expect[id] = struct{}{}
	}
	for _, id := range actualIDs {
		if _, ok := expect[id]; ok {
			return true
		}
	}
	return false
}

// evalCitationOK 引用正确性判定（纯函数）：引用非空 且 引用文章集与期望集有
// 交集——回答引用了期望文章中的至少一篇即通过，与 evalHit 同口径对称。
// 夹带期望外文章/幻觉引用不因此判负（如需度量在 detail 侧观察，不进主指标）。
func evalCitationOK(expectIDs, citationIDs []uint) bool {
	if len(citationIDs) == 0 {
		return false
	}
	expect := make(map[uint]struct{}, len(expectIDs))
	for _, id := range expectIDs {
		expect[id] = struct{}{}
	}
	for _, id := range citationIDs {
		if _, ok := expect[id]; ok {
			return true
		}
	}
	return false
}

// answerCitationPattern 回答正文中的引用标注 [n]（n 为上下文分组序号，
// 见 rag.go ask 的组装约定）。RE2 无 lookbehind，前缀是否为 \w 字符
// （代码下标 a[1]）由 parseAnswerCitationIdx 逐个检查前一字节，正则保持简单。
var answerCitationPattern = regexp.MustCompile(`\[(\d{1,2})\]`)

// parseAnswerCitationIdx 提取回答正文中出现的引用序号（去重保序，纯函数）。
// 前一字符是字母/数字/下划线的 [n] 视为代码下标（如 a[1]）跳过；
// 是否越界（幻觉引用）由调用方结合分组数判定。
func parseAnswerCitationIdx(answer string) []int {
	seen := make(map[int]struct{}, 4)
	idx := make([]int, 0, 4)
	for _, loc := range answerCitationPattern.FindAllStringSubmatchIndex(answer, -1) {
		if start := loc[0]; start > 0 && isWordByte(answer[start-1]) {
			continue
		}
		n, err := strconv.Atoi(answer[loc[2]:loc[3]])
		if err != nil {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		idx = append(idx, n)
	}
	return idx
}

// isWordByte ASCII 的字母/数字/下划线（正则 \w 的字节集；多字节字符的尾字节 ≥0x80 不属于）。
func isWordByte(b byte) bool {
	return b == '_' || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9')
}

// ---------------- slug → 文章 ID 解析 ----------------

// articleIDBySlug 批量查 slug → 文章 ID。
func (s *RagEvalService) articleIDBySlug(ctx context.Context, slugs []string) (map[string]uint, error) {
	m := make(map[string]uint, len(slugs))
	if len(slugs) == 0 {
		return m, nil
	}
	var rows []model.Article
	if err := s.db.WithContext(ctx).Model(&model.Article{}).Select("id, slug").
		Where("slug IN ?", slugs).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("按 slug 查文章: %w", err)
	}
	for _, r := range rows {
		m[r.Slug] = r.ID
	}
	return m, nil
}

// articleIDsForSlugs 把 slug 序列解析为文章 ID 序列：cached 优先命中，缺失的
// 批量补查并回填 cached；仍解析不到的以 0 占位（向量残留/文章已删，告警留痕）——
// 0 永不在合法期望集内：引用侧直接参与判定自然不通过，检索侧由调用方过滤。
func (s *RagEvalService) articleIDsForSlugs(ctx context.Context, cached map[string]uint, slugs []string) ([]uint, error) {
	missing := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		if _, ok := cached[slug]; !ok {
			missing = append(missing, slug)
		}
	}
	if len(missing) > 0 {
		extra, err := s.articleIDBySlug(ctx, missing)
		if err != nil {
			return nil, err
		}
		for k, v := range extra {
			cached[k] = v
		}
	}
	ids := make([]uint, 0, len(slugs))
	for _, slug := range slugs {
		if id, ok := cached[slug]; ok {
			ids = append(ids, id)
			continue
		}
		s.log.Warn("slug 解析不到文章（向量残留或文章已删除）", zap.String("slug", slug))
		ids = append(ids, 0)
	}
	return ids, nil
}

// ---------------- 评测轮查询（HTTP 只读接口） ----------------

// 评测轮列表查询参数：limit 钳制区间与列表字段
//（逐条明细 detail 是较大的 JSON 数组，列表不携带，详情走 GetRun）。
const (
	evalRunListDefaultLimit = 20
	evalRunListMaxLimit     = 50
	evalRunListColumns      = "id, status, total, hit_count, hit_rate, citation_ok_rate, avg_judge_score, error, created_at"
)

// ListRuns 最近评测轮列表（id 倒序）：limit<=0 取 20，上限 50（与 Daily 天数钳制同风格）。
func (s *RagEvalService) ListRuns(ctx context.Context, limit int) ([]model.RagEvalRun, error) {
	if limit <= 0 {
		limit = evalRunListDefaultLimit
	}
	if limit > evalRunListMaxLimit {
		limit = evalRunListMaxLimit
	}
	runs := make([]model.RagEvalRun, 0)
	if err := s.db.WithContext(ctx).Select(evalRunListColumns).
		Order("id DESC").Limit(limit).Find(&runs).Error; err != nil {
		return nil, fmt.Errorf("查询评测轮列表: %w", err)
	}
	return runs, nil
}

// GetRun 单个评测轮详情（含逐条明细 detail）。
func (s *RagEvalService) GetRun(ctx context.Context, id uint) (*model.RagEvalRun, error) {
	var run model.RagEvalRun
	if err := s.db.WithContext(ctx).First(&run, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: 评测轮不存在", ErrNotFound)
		}
		return nil, fmt.Errorf("查询评测轮: %w", err)
	}
	return &run, nil
}
