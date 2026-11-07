package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"blog/server/internal/llm"
	"blog/server/internal/mailer"
	"blog/server/internal/model"
	"blog/server/internal/svggen"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// articleListColumns 列表查询字段（排除大字段 content_md）。
const articleListColumns = "id, repo_analysis_id, title, slug, summary, word_count, like_count, tags, " +
	"status, sort_order, cover_asset_id, published_at, created_at, updated_at, review_score"

// ArticleService 文章的查询、编辑、发版/下线与配图重生成。
type ArticleService struct {
	db     *gorm.DB
	rdb    *redis.Client
	llm    *llm.Client
	log    *zap.Logger
	portal *PortalService // 复用缓存失效逻辑
	mailer *mailer.Mailer // 发布成功后的邮件通知
	rag    *RagService    // RAG 技术问答：发布索引 / 下线删除（可为 nil）
}

// NewArticleService 创建文章服务。
func NewArticleService(db *gorm.DB, rdb *redis.Client, client *llm.Client,
	notify *mailer.Mailer, rag *RagService, log *zap.Logger) *ArticleService {
	return &ArticleService{db: db, rdb: rdb, llm: client, log: log,
		portal: NewPortalService(db, rdb, rag, log), mailer: notify, rag: rag}
}

// FigureMeta 文章配图元数据（不含 SVG 原文）。
type FigureMeta struct {
	ID          uint      `json:"id"`
	ArticleID   uint      `json:"article_id"`
	Placeholder string    `json:"placeholder"`
	Kind        string    `json:"kind"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
	// Message 配图修改的实际变化摘要（仅修改流程填写）：改了标题/
	// 配色/数据哪些维度，或明确告知未产生变化；其余场景为空。
	Message string `json:"message,omitempty"`
}

// FigurePreview 配图 AI 修改的预览结果（未落库）：AI 只产出候选版本，
// 是否采纳由用户决定——点「应用」走 ApplyFigureChange 才会更新文章。
type FigurePreview struct {
	Title   string `json:"title"`
	Spec    string `json:"spec"` // 新的图数据 JSON 文本（含可选 theme 字段）
	SVG     string `json:"svg"`  // 预览渲染产物（确定性渲染，应用后与之一致）
	Message string `json:"message"`
}

// ArticleDetail 文章详情（含配图元数据数组）。
type ArticleDetail struct {
	model.Article
	Figures []FigureMeta `json:"figures"`
}

// List 文章列表，status 为空时返回全部。
func (s *ArticleService) List(ctx context.Context, status string, page, pageSize int) (*PageData, error) {
	page, pageSize = NormalizePage(page, pageSize)
	q := s.db.WithContext(ctx).Model(&model.Article{})
	if status = strings.TrimSpace(status); status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("统计文章数: %w", err)
	}
	items := make([]model.Article, 0)
	err := q.Select(articleListColumns).Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("查询文章列表: %w", err)
	}
	return &PageData{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

// Get 文章详情，含全部配图（含封面）元数据。
func (s *ArticleService) Get(ctx context.Context, id uint) (*ArticleDetail, error) {
	var art model.Article
	err := s.db.WithContext(ctx).First(&art, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: 文章 %d 不存在", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询文章 %d: %w", id, err)
	}
	figs, err := s.figures(ctx, art.ID)
	if err != nil {
		return nil, err
	}
	return &ArticleDetail{Article: art, Figures: figs}, nil
}

// Update 编辑文章（标题/摘要/正文/标签），重算字数；
// 已发布文章保存后回到 draft，需重新发版，并失效前台缓存。
func (s *ArticleService) Update(ctx context.Context, id uint, title, summary, contentMD string, tags []string) (*model.Article, error) {
	title = truncateRunes(strings.TrimSpace(title), 256)
	if title == "" {
		return nil, fmt.Errorf("%w: 标题不能为空", ErrInvalid)
	}
	var art model.Article
	err := s.db.WithContext(ctx).First(&art, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: 文章 %d 不存在", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询文章 %d: %w", id, err)
	}

	wasPublished := art.Status == model.ArticleStatusPublished
	art.Title = title
	art.Summary = truncateRunes(summary, 512)
	art.ContentMD = contentMD
	art.Tags = jsonifyTags(tags)
	art.WordCount = CountWords(contentMD)
	if wasPublished {
		art.Status = model.ArticleStatusDraft
	}
	if err := s.db.WithContext(ctx).Save(&art).Error; err != nil {
		return nil, fmt.Errorf("保存文章 %d: %w", id, err)
	}
	// 原先对外可见（已发布）时才需失效前台缓存
	if wasPublished {
		s.portal.InvalidateCache(ctx, art.Slug)
		// 已发布文章被编辑退回草稿：内容将改变，向量即刻作废
		if s.rag != nil {
			s.rag.RemoveArticle(ctx, art.ID)
		}
	}
	s.log.Info("文章已编辑", zap.Uint("article_id", id), zap.Bool("was_published", wasPublished))
	return &art, nil
}

// Publish 发版：draft → published，写 published_at 并失效前台缓存；
// 成功后异步邮件通知发布者（通知失败不影响发布结果）。
func (s *ArticleService) Publish(ctx context.Context, id, publisherID uint) (*model.Article, error) {
	var art model.Article
	err := s.db.WithContext(ctx).First(&art, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: 文章 %d 不存在", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询文章 %d: %w", id, err)
	}
	if art.Status != model.ArticleStatusDraft {
		return nil, fmt.Errorf("%w: 仅待审核文章可发版，当前状态为 %s", ErrConflict, art.Status)
	}
	// 发布防线：正文占位符必须都有对应配图——悬空占位符会在前台原样暴露给读者
	if missing := s.danglingPlaceholders(ctx, &art); len(missing) > 0 {
		return nil, fmt.Errorf("%w: 正文占位符 %s 无对应配图，请先在编辑页「重新生成配图」补齐",
			ErrInvalid, strings.Join(missing, ", "))
	}
	now := time.Now()
	if err := s.db.WithContext(ctx).Model(&art).Updates(map[string]any{
		"status":       model.ArticleStatusPublished,
		"published_at": now,
	}).Error; err != nil {
		return nil, fmt.Errorf("发布文章 %d: %w", id, err)
	}
	s.portal.InvalidateCache(ctx, art.Slug)
	// 异步邮件通知发布者（mailer 内部处理长文章生成时的重复通知）...
	if s.mailer != nil {
		s.mailer.NotifyArticlePublished(publisherID, art.Title, art.Slug, now)
	}
	// 异步向量化入库，供 RAG 技术问答检索（失败仅记日志，不影响发布结果）
	if s.rag != nil {
		s.indexForRAG(ctx, art)
	}
	s.log.Info("文章已发版", zap.Uint("article_id", id), zap.String("slug", art.Slug))
	art.Status = model.ArticleStatusPublished
	art.PublishedAt = &now
	return &art, nil
}

// indexForRAG 异步向量化入库：与请求上下文解耦（HTTP 返回后仍继续），
// 限时 60s；失败仅记日志，不影响发布结果。传值拷贝避免与调用方后续字段赋值竞争。
func (s *ArticleService) indexForRAG(ctx context.Context, art model.Article) {
	go func() {
		ictx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		if err := s.rag.IndexArticle(ictx, &art); err != nil {
			s.log.Warn("文章向量化入库失败（问答暂不可检索该文）",
				zap.Uint("article_id", art.ID), zap.String("slug", art.Slug), zap.Error(err))
		}
	}()
}

// Offline 下线：published → draft，失效前台缓存。
func (s *ArticleService) Offline(ctx context.Context, id uint) (*model.Article, error) {
	var art model.Article
	err := s.db.WithContext(ctx).First(&art, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: 文章 %d 不存在", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询文章 %d: %w", id, err)
	}
	if art.Status != model.ArticleStatusPublished {
		return nil, fmt.Errorf("%w: 仅已发布文章可下线，当前状态为 %s", ErrConflict, art.Status)
	}
	if err := s.db.WithContext(ctx).Model(&art).Update("status", model.ArticleStatusDraft).Error; err != nil {
		return nil, fmt.Errorf("下线文章 %d: %w", id, err)
	}
	s.portal.InvalidateCache(ctx, art.Slug)
	// 下线即对外不可见：同步删除 RAG 向量，问答不再引用
	if s.rag != nil {
		s.rag.RemoveArticle(ctx, art.ID)
	}
	s.log.Info("文章已下线", zap.Uint("article_id", id), zap.String("slug", art.Slug))
	art.Status = model.ArticleStatusDraft
	return &art, nil
}

// RegenerateFigures 重新生成文章配图：正文中存在悬空占位符（无对应配图）时
// 优先走补齐路径；否则读取现有非 cover 配图元数据，调 LLM 重新设计图表数据，
// 逐张重渲染并更新 spec/svg_content；LLM 未返回的旧图保持不变。
func (s *ArticleService) RegenerateFigures(ctx context.Context, id uint) (*ArticleDetail, error) {
	var art model.Article
	err := s.db.WithContext(ctx).First(&art, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: 文章 %d 不存在", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询文章 %d: %w", id, err)
	}
	var assets []model.SvgAsset
	if err := s.db.WithContext(ctx).
		Where("article_id = ? AND kind <> ?", art.ID, model.FigureKindCover).
		Order("id ASC").Find(&assets).Error; err != nil {
		return nil, fmt.Errorf("查询文章 %d 配图: %w", id, err)
	}
	// 悬空占位符优先补齐：生成期渲染失败被跳过的图会让占位符原样暴露到前台，
	// 且旧逻辑在 0 张配图时直接报错，悬空后无法自救；补齐不动已有配图
	if missing := missingPlaceholders(art.ContentMD, assets); len(missing) > 0 {
		return s.fillMissingFigures(ctx, &art, missing)
	}
	if len(assets) == 0 {
		return nil, fmt.Errorf("%w: 文章《%s》正文无配图占位符，无需重新生成", ErrInvalid, art.Title)
	}

	byPlaceholder := make(map[string]*model.SvgAsset, len(assets))
	meta := make([]string, 0, len(assets))
	for i := range assets {
		a := &assets[i]
		byPlaceholder[a.Placeholder] = a
		meta = append(meta, fmt.Sprintf("- id: %s\n  kind: %s\n  title: %s", a.Placeholder, a.Kind, a.Title))
	}

	system, user := llm.BuildFigureRegenPrompt(art.Title, art.ContentMD, strings.Join(meta, "\n"))
	var out llm.FiguresOut
	err = s.llm.ChatJSON(ctx, model.LlmSceneRefigure, system, user, &out, func() error {
		seen := map[string]bool{}
		for i, f := range out.Figures {
			if strings.TrimSpace(f.ID) == "" {
				return fmt.Errorf("figures[%d].id 不能为空", i)
			}
			if seen[f.ID] {
				return fmt.Errorf("figures[%d].id 重复: %s", i, f.ID)
			}
			seen[f.ID] = true
			if _, ok := byPlaceholder[f.ID]; !ok {
				return fmt.Errorf("figures[%d].id 不在已有配图清单中: %s", i, f.ID)
			}
			if len(f.Data) == 0 {
				return fmt.Errorf("figures[%d].data 不能为空", i)
			}
			// 剥掉 LLM 偶发多包的一层类型键，避免包装数据渲染成空图
			out.Figures[i].Data = unwrapFigureData(byPlaceholder[f.ID].Kind, f.Data)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("重新生成配图: %w", err)
	}

	updated := 0
	for _, f := range out.Figures {
		asset := byPlaceholder[f.ID]
		title := strings.TrimSpace(f.Title)
		if title == "" {
			title = asset.Title
		}
		svg, rerr := svggen.Render(asset.Kind, title, string(f.Data))
		if rerr != nil {
			// 渲染是确定性流程，失败保留原图
			s.log.Warn("重渲染配图失败，保留原图",
				zap.Uint("article_id", art.ID), zap.String("figure", f.ID), zap.Error(rerr))
			continue
		}
		spec := llm.FigureOut{ID: asset.Placeholder, Kind: string(asset.Kind), Title: title, Data: f.Data}
		if uerr := s.db.WithContext(ctx).Model(&model.SvgAsset{}).
			Where("id = ?", asset.ID).
			Updates(map[string]any{"title": title, "spec": jsonify(spec), "svg_content": svg}).
			Error; uerr != nil {
			return nil, fmt.Errorf("更新配图 %s: %w", f.ID, uerr)
		}
		asset.Title = title
		updated++
	}
	s.portal.InvalidateCache(ctx, art.Slug)
	s.log.Info("配图重新生成完成",
		zap.Uint("article_id", art.ID), zap.Int("total", len(assets)), zap.Int("updated", updated))
	return s.Get(ctx, id)
}

// fillMissingFigures 为正文中悬空的占位符补齐配图：kind 由模型按上下文自选，
// 单张渲染失败跳过并留痕，不阻塞其余补齐——仍未补齐的会被发布校验拦住。
func (s *ArticleService) fillMissingFigures(ctx context.Context, art *model.Article, missing []string) (*ArticleDetail, error) {
	want := make(map[string]bool, len(missing))
	for _, id := range missing {
		want[id] = true
	}
	system, user := llm.BuildFigureFillPrompt(art.Title, art.ContentMD, strings.Join(missing, ", "))
	var out llm.FiguresOut
	err := s.llm.ChatJSON(ctx, model.LlmSceneRefigure, system, user, &out, func() error {
		seen := map[string]bool{}
		for i, f := range out.Figures {
			if strings.TrimSpace(f.ID) == "" {
				return fmt.Errorf("figures[%d].id 不能为空", i)
			}
			if seen[f.ID] {
				return fmt.Errorf("figures[%d].id 重复: %s", i, f.ID)
			}
			if !want[f.ID] {
				return fmt.Errorf("figures[%d].id 不在缺失清单中: %s", i, f.ID)
			}
			seen[f.ID] = true
			switch model.FigureKind(f.Kind) {
			case model.FigureKindArchitecture, model.FigureKindFlow,
				model.FigureKindCompare, model.FigureKindTimeline:
			default:
				return fmt.Errorf("figures[%d].kind 非法: %s", i, f.Kind)
			}
			if len(f.Data) == 0 {
				return fmt.Errorf("figures[%d].data 不能为空", i)
			}
			// 剥掉 LLM 偶发多包的一层类型键，避免包装数据渲染成空图
			out.Figures[i].Data = unwrapFigureData(model.FigureKind(f.Kind), f.Data)
		}
		// 模型偶发漏项：缺一张就整体打回重试（ChatJSON 会带错误重问），
		// 避免「调多次碰运气」式补图
		for _, id := range missing {
			if !seen[id] {
				return fmt.Errorf("figures 缺少缺失清单中的: %s", id)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("补齐配图: %w", err)
	}

	created := 0
	for _, f := range out.Figures {
		kind := model.FigureKind(f.Kind)
		title := strings.TrimSpace(f.Title)
		if title == "" {
			title = f.ID
		}
		svg, rerr := svggen.Render(kind, title, string(f.Data))
		if rerr != nil {
			// 渲染是确定性流程，失败仅跳过该占位符，不阻塞其余补齐
			s.log.Warn("补齐配图渲染失败，跳过",
				zap.Uint("article_id", art.ID), zap.String("figure", f.ID), zap.Error(rerr))
			continue
		}
		spec := llm.FigureOut{ID: f.ID, Kind: string(kind), Title: title, Data: f.Data}
		asset := &model.SvgAsset{
			ArticleID:   art.ID,
			Placeholder: f.ID,
			Kind:        kind,
			Title:       title,
			Spec:        jsonify(spec),
			SVGContent:  svg,
		}
		if uerr := s.db.WithContext(ctx).Create(asset).Error; uerr != nil {
			return nil, fmt.Errorf("保存配图 %s: %w", f.ID, uerr)
		}
		created++
	}
	s.portal.InvalidateCache(ctx, art.Slug)
	s.log.Info("配图补齐完成",
		zap.Uint("article_id", art.ID), zap.Int("missing", len(missing)), zap.Int("created", created))
	return s.Get(ctx, art.ID)
}

// missingPlaceholders 找出正文占位符中没有对应配图的 id（去重、保序）。
// 前台按 placeholder 匹配替换配图，匹配不到的占位符会原样暴露给读者。
func missingPlaceholders(md string, assets []model.SvgAsset) []string {
	have := make(map[string]bool, len(assets))
	for i := range assets {
		have[assets[i].Placeholder] = true
	}
	var missing []string
	seen := map[string]bool{}
	for _, m := range figurePlaceholderRe.FindAllStringSubmatch(md, -1) {
		id := strings.TrimSpace(m[1])
		if id == "" || seen[id] || have[id] {
			continue
		}
		seen[id] = true
		missing = append(missing, id)
	}
	return missing
}

// danglingPlaceholders 返回正文中没有对应非 cover 配图的悬空占位符，
// 口径与前台一致（portal 只按非 cover 配图替换）；查询失败时返回空放行并记
// 日志——发布校验是防线而非绝对保障，不应因 DB 抖动卡死发布。
func (s *ArticleService) danglingPlaceholders(ctx context.Context, art *model.Article) []string {
	var assets []model.SvgAsset
	if err := s.db.WithContext(ctx).
		Where("article_id = ? AND kind <> ?", art.ID, model.FigureKindCover).
		Find(&assets).Error; err != nil {
		s.log.Warn("发布校验查询配图失败，放行",
			zap.Uint("article_id", art.ID), zap.Error(err))
		return nil
	}
	return missingPlaceholders(art.ContentMD, assets)
}

// unwrapFigureData 剥掉 LLM 偶发多包的一层类型键：模型有时输出
// {"flow":{"steps":[...]}} 而非顶层 {"steps":[...]}，渲染与内容校验都按
// 顶层结构解析，包装结构会被当成空数据渲染出空图。仅当顶层唯一键
// 恰为 kind 名时才剥内层，其余原样返回。
func unwrapFigureData(kind model.FigureKind, data []byte) []byte {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil || len(probe) != 1 {
		return data
	}
	inner, ok := probe[string(kind)]
	if !ok {
		return data
	}
	return inner
}

// themeColorRules 主题色词 → 预设主题；长词在前优先匹配（「深空蓝」先于「蓝」）。
var themeColorRules = []struct{ word, theme string }{
	{"深空蓝", "midnight"}, {"深蓝", "midnight"}, {"深色", "midnight"},
	{"森林绿", "forest"},
	{"海洋蓝", "ocean"}, {"海蓝", "ocean"},
	{"浅色", "light"}, {"浅底", "light"},
	{"蓝", "ocean"}, {"绿", "forest"}, {"白", "light"}, {"亮", "light"}, {"暗", "midnight"},
}

// themeSignalWords 配色类指令的信号词，须与色词同时出现才走直路由。
var themeSignalWords = []string{"背景", "配色", "颜色", "色调", "主题"}

// hasThemeSignal 指令是否含配色信号词。
func hasThemeSignal(instruction string) bool {
	for _, w := range themeSignalWords {
		if strings.Contains(instruction, w) {
			return true
		}
	}
	return false
}

// titleSignalWords 标题类指令的信号词：指令未提标题时不允许 LLM 改标题。
var titleSignalWords = []string{"标题", "题目", "图名", "改名", "命名"}

// hasTitleSignal 指令是否涉及标题。
func hasTitleSignal(instruction string) bool {
	for _, w := range titleSignalWords {
		if strings.Contains(instruction, w) {
			return true
		}
	}
	return false
}

// themeDirectRoute 纯配色指令直路由判定：命中返回目标主题。
// 换配色是确定性操作，不过 LLM——实测让模型「原样回传数据只改一处」
// 会顺手改坏数据（如把 7 步精简成 4 步）。
func themeDirectRoute(instruction string) (string, bool) {
	if !hasThemeSignal(instruction) {
		return "", false
	}
	for _, r := range themeColorRules {
		if strings.Contains(instruction, r.word) {
			return r.theme, true
		}
	}
	return "", false
}

// mergeTheme 把 theme 写入 data JSON（覆盖已有值）；解析失败原样返回。
func mergeTheme(data []byte, theme string) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return data
	}
	tb, err := json.Marshal(theme)
	if err != nil {
		return data
	}
	m["theme"] = tb
	out, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return out
}

// stripThemeFrom 返回剔除 theme 字段后的 JSON（无 theme 或解析失败返回 false）。
func stripThemeFrom(s string) (string, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(s), &m) != nil {
		return "", false
	}
	if _, existed := m["theme"]; !existed {
		return "", false
	}
	delete(m, "theme")
	b, err := json.Marshal(m)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// sameDataIgnoreTheme 数据对比剔除 theme 字段（单边无 theme 亦可比）：
// 纯换配色不虚报「数据已更新」。
func sameDataIgnoreTheme(a, b string) bool {
	ra, ok := stripThemeFrom(a)
	if !ok {
		ra = a
	}
	rb, ok := stripThemeFrom(b)
	if !ok {
		rb = b
	}
	return sameJSON(ra, rb)
}

// ModifyFigure 按指令 AI 修改单张配图：当前数据 + 指令交 LLM 改写 spec，
// 确定性重渲染后产出预览（placeholder 不变，正文无需改）。
// 只生成候选版本不落库——用户确认后调 ApplyFigureChange 才真正更新文章；
// LLM 失败或新数据渲染失败时原图保持不变。
func (s *ArticleService) ModifyFigure(ctx context.Context, id, figureID uint, instruction string) (*FigurePreview, error) {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return nil, fmt.Errorf("%w: 修改指令不能为空", ErrInvalid)
	}
	if n := len([]rune(instruction)); n > 200 {
		return nil, fmt.Errorf("%w: 修改指令不能超过 200 字（当前 %d 字）", ErrInvalid, n)
	}
	var art model.Article
	err := s.db.WithContext(ctx).First(&art, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: 文章 %d 不存在", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询文章 %d: %w", id, err)
	}
	var asset model.SvgAsset
	err = s.db.WithContext(ctx).Where("article_id = ? AND id = ?", id, figureID).First(&asset).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: 配图 %d 不存在", ErrNotFound, figureID)
	}
	if err != nil {
		return nil, fmt.Errorf("查询配图 %d: %w", figureID, err)
	}
	if asset.Kind == model.FigureKindCover {
		return nil, fmt.Errorf("%w: 封面不支持按指令修改（随正文重新生成）", ErrInvalid)
	}
	// 当前 spec 的 data 是 LLM 的修改基线（剥掉可能的历史包装层）
	var cur llm.FigureOut
	if err := json.Unmarshal(asset.Spec, &cur); err != nil {
		return nil, fmt.Errorf("解析配图 %d 当前数据: %w", figureID, err)
	}
	cur.Data = unwrapFigureData(asset.Kind, cur.Data)

	// 纯配色指令直路由：确定性操作不过 LLM，直接换 theme 重渲染出预览，
	// 数据与标题零风险、秒回
	if theme, ok := themeDirectRoute(instruction); ok {
		return s.previewFigureTheme(&asset, string(cur.Data), theme)
	}

	// 若基线已损坏（主体条目为空，
	// 如历史 bug 改出的空 steps），「在现有基础上最小改动」只会继续输出空，
	// 切到重建模式：不传坏数据，让模型依据正文 + 指令全新设计。
	var system, user string
	if svggen.HasContent(asset.Kind, string(cur.Data)) {
		system, user = llm.BuildFigureModifyPrompt(art.Title, art.ContentMD, string(asset.Kind), asset.Title, string(cur.Data), instruction)
	} else {
		system, user = llm.BuildFigureRebuildPrompt(art.Title, art.ContentMD, string(asset.Kind), asset.Title, instruction)
	}
	var out llm.FigureModifyOut
	err = s.llm.ChatJSON(ctx, model.LlmSceneRefigure, system, user, &out, func() error {
		if len(out.Data) == 0 {
			return fmt.Errorf("data 不能为空")
		}
		// LLM 偶发把 data 多包一层类型键（如 {"flow":{"steps":[...]}}），先归一
		out.Data = unwrapFigureData(asset.Kind, out.Data)
		// 拦下「改空」结果：steps/layers 等主体条目为空会渲染成只剩标题的空图，
		// 且坏数据会成为下一轮修改的基线，图就再也改不回来了
		if !svggen.HasContent(asset.Kind, string(out.Data)) {
			return fmt.Errorf("data 缺少主体内容（如 flow 至少 1 个 steps），不允许清空图内容")
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("AI 修改配图: %w", err)
	}

	// 复合指令带出的 theme 校验合法后并入 data；指令本身无配色信号时
	// 忽略（实测内容指令模型也会擅自输出 theme，覆盖既有配色），
	// 并继承当前 data 的显式 theme——否则渲染回退默认深空蓝，
	// 纯内容修改会丢掉用户已确认的配色
	if t := strings.TrimSpace(out.Theme); t != "" && hasThemeSignal(instruction) && svggen.IsValidTheme(t) {
		out.Data = mergeTheme(out.Data, t)
	} else if _, has := stripThemeFrom(string(cur.Data)); has {
		out.Data = mergeTheme(out.Data, figureTheme(string(cur.Data)))
	}

	// 标题护栏：指令未涉及标题时强制沿用原标题（实测内容指令模型
	// 也会顺手润色标题，如「分层」改「完整」）
	title := strings.TrimSpace(out.Title)
	if title == "" || !hasTitleSignal(instruction) {
		title = asset.Title
	}
	svg, rerr := svggen.Render(asset.Kind, title, string(out.Data))
	if rerr != nil {
		// 单图定向修改只有一张：渲染失败须明确告知本次未生效，原图已保留
		return nil, fmt.Errorf("%w: 新数据渲染失败（%v），原图已保留", ErrInvalid, rerr)
	}
	return &FigurePreview{
		Title:   title,
		Spec:    string(out.Data),
		SVG:     svg,
		Message: figureChangeSummary(title, asset.Title, string(cur.Data), string(out.Data)),
	}, nil
}

// ApplyFigureChange 将用户确认的配图修改落到文章：title/spec 为预览阶段
// 返回的候选版本，后端重新校验并确定性重渲染（同输入同输出，与预览一致）
// 后落库。用户「放弃」则什么都不发生，原图保持不变。
func (s *ArticleService) ApplyFigureChange(ctx context.Context, id, figureID uint, title, spec string) (*FigureMeta, error) {
	title = truncateRunes(strings.TrimSpace(title), 256)
	if title == "" {
		return nil, fmt.Errorf("%w: 图标题不能为空", ErrInvalid)
	}
	spec = strings.TrimSpace(spec)
	if spec == "" || !json.Valid([]byte(spec)) {
		return nil, fmt.Errorf("%w: 图数据必须是合法 JSON", ErrInvalid)
	}
	var art model.Article
	err := s.db.WithContext(ctx).First(&art, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: 文章 %d 不存在", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询文章 %d: %w", id, err)
	}
	var asset model.SvgAsset
	err = s.db.WithContext(ctx).Where("article_id = ? AND id = ?", id, figureID).First(&asset).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: 配图 %d 不存在", ErrNotFound, figureID)
	}
	if err != nil {
		return nil, fmt.Errorf("查询配图 %d: %w", figureID, err)
	}
	if asset.Kind == model.FigureKindCover {
		return nil, fmt.Errorf("%w: 封面不支持按指令修改（随正文重新生成）", ErrInvalid)
	}
	data := unwrapFigureData(asset.Kind, []byte(spec))
	// 预览虽已校验过，应用入口仍须重拦：回传数据可能被篡改或损坏，
	// 空数据落库会成为下一轮修改的坏基线
	if !svggen.HasContent(asset.Kind, string(data)) {
		return nil, fmt.Errorf("%w: 图数据缺少主体内容，拒绝应用", ErrInvalid)
	}
	svg, rerr := svggen.Render(asset.Kind, title, string(data))
	if rerr != nil {
		return nil, fmt.Errorf("%w: 图数据渲染失败（%v），原图已保留", ErrInvalid, rerr)
	}
	specFull := llm.FigureOut{ID: asset.Placeholder, Kind: string(asset.Kind), Title: title, Data: data}
	if uerr := s.db.WithContext(ctx).Model(&model.SvgAsset{}).
		Where("id = ?", asset.ID).
		Updates(map[string]any{"title": title, "spec": jsonify(specFull), "svg_content": svg}).
		Error; uerr != nil {
		return nil, fmt.Errorf("更新配图 %d: %w", figureID, uerr)
	}
	s.portal.InvalidateCache(ctx, art.Slug)
	s.log.Info("配图修改已确认应用",
		zap.Uint("article_id", art.ID), zap.Uint("figure_id", asset.ID))
	return &FigureMeta{
		ID:          asset.ID,
		ArticleID:   art.ID,
		Placeholder: asset.Placeholder,
		Kind:        string(asset.Kind),
		Title:       title,
		CreatedAt:   asset.CreatedAt,
		Message:     "修改已应用到文章",
	}, nil
}

// previewFigureTheme 纯配色直路由的预览实现：theme 并入 data 后重渲染，
// 标题与数据内容一律不动。
func (s *ArticleService) previewFigureTheme(asset *model.SvgAsset, curData, theme string) (*FigurePreview, error) {
	newData := mergeTheme([]byte(curData), theme)
	svg, rerr := svggen.Render(asset.Kind, asset.Title, string(newData))
	if rerr != nil {
		return nil, fmt.Errorf("%w: 主题渲染失败（%v），原图已保留", ErrInvalid, rerr)
	}
	return &FigurePreview{
		Title:   asset.Title,
		Spec:    string(newData),
		SVG:     svg,
		Message: fmt.Sprintf("配色切换为%s，数据与标题未动", themeNames[theme]),
	}, nil
}

// themeNames 配色主题的展示名（与 svggen themes 的 key 对应）。
var themeNames = map[string]string{
	"midnight": "深空蓝",
	"ocean":    "海洋蓝",
	"light":    "浅色",
	"forest":   "森林绿",
}

// figureTheme 取 data 里可选 theme 字段（缺省 midnight）。
func figureTheme(data string) string {
	var meta struct {
		Theme string `json:"theme"`
	}
	if json.Unmarshal([]byte(data), &meta) == nil && meta.Theme != "" {
		return meta.Theme
	}
	return "midnight"
}

// figureChangeSummary 生成如实的修改结果摘要：逐维度对比新旧值，
// 没有变化的维度不虚报——前端原样展示，杜绝「假装成功」的误导。
func figureChangeSummary(newTitle, oldTitle, oldData, newData string) string {
	var parts []string
	if newTitle != oldTitle {
		parts = append(parts, fmt.Sprintf("标题更新为「%s」", newTitle))
	}
	if ot, nt := figureTheme(oldData), figureTheme(newData); ot != nt {
		parts = append(parts, fmt.Sprintf("配色切换为%s", themeNames[nt]))
	}
	if !sameDataIgnoreTheme(oldData, newData) {
		parts = append(parts, "图数据已更新")
	}
	if len(parts) == 0 {
		return "本次指令未对图产生变化（配色与版式模板固定，指令或不在可修改范围内）"
	}
	return "本次修改：" + strings.Join(parts, "，") + "。请查看预览图，确认后才会更新到文章"
}

// sameJSON 语义级比较两段 JSON（键序与空白差异不算变化）。
func sameJSON(a, b string) bool {
	var va, vb any
	if json.Unmarshal([]byte(a), &va) != nil || json.Unmarshal([]byte(b), &vb) != nil {
		return a == b
	}
	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)
	return bytes.Equal(ja, jb)
}

// figures 查询文章全部配图元数据（含封面）。
func (s *ArticleService) figures(ctx context.Context, articleID uint) ([]FigureMeta, error) {
	figs := make([]FigureMeta, 0)
	err := s.db.WithContext(ctx).Model(&model.SvgAsset{}).
		Select("id, article_id, placeholder, kind, title, created_at").
		Where("article_id = ?", articleID).
		Order("id ASC").Find(&figs).Error
	if err != nil {
		return nil, fmt.Errorf("查询文章 %d 配图: %w", articleID, err)
	}
	return figs, nil
}

// ListReviews 文章的 LLM 评审记录（created_at 倒序，最多 10 条）：
// 评审按轮追加落库，管理端审核参考最近若干条即可。
func (s *ArticleService) ListReviews(ctx context.Context, articleID uint) ([]model.ArticleReview, error) {
	reviews := make([]model.ArticleReview, 0)
	err := s.db.WithContext(ctx).Where("article_id = ?", articleID).
		Order("created_at DESC").Limit(10).Find(&reviews).Error
	if err != nil {
		return nil, fmt.Errorf("查询文章 %d 评审记录: %w", articleID, err)
	}
	return reviews, nil
}

// CountWords 统计字数：CJK 字符按字计，连续拉丁字母/数字串按词计。
func CountWords(s string) int {
	n := 0
	inWord := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			n++
			inWord = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !inWord {
				n++
				inWord = true
			}
		default:
			inWord = false
		}
	}
	return n
}

// jsonifyTags 标签数组序列化为 model.JSON（去掉空白项）。
func jsonifyTags(tags []string) model.JSON {
	clean := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			clean = append(clean, t)
		}
	}
	return jsonify(clean)
}

// jsonify 序列化为 model.JSON（存库用），失败返回空对象。
func jsonify(v any) model.JSON {
	b, err := json.Marshal(v)
	if err != nil {
		return model.JSON("{}")
	}
	return model.JSON(b)
}

// truncateRunes 按字符数截断字符串。
func truncateRunes(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n])
}
