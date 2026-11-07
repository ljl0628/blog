package api

import (
	"blog/server/internal/httputil"
	"blog/server/internal/middleware"
	"blog/server/internal/resp"
	"blog/server/internal/service"

	"github.com/gin-gonic/gin"
)

// ArticleHandler 文章管理接口（admin）。
type ArticleHandler struct {
	svc *service.ArticleService
}

// NewArticleHandler 创建文章 handler。
func NewArticleHandler(svc *service.ArticleService) *ArticleHandler { return &ArticleHandler{svc: svc} }

// List GET /api/v1/articles?status=&page=&page_size=：文章分页列表。
//
//	@Summary 文章分页列表（管理端）
//	@Tags 文章
//	@Security BearerAuth
//	@Produce json
//	@Param status query string false "状态过滤：draft / published，可省略"
//	@Param page query int false "页码，默认 1"
//	@Param page_size query int false "每页条数，默认 10，上限 100"
//	@Success 200 {object} resp.Envelope{data=service.PageData{items=[]model.Article}}
//	@Router /articles [get]
func (h *ArticleHandler) List(c *gin.Context) {
	page, pageSize := httputil.PageParams(c)
	pd, err := h.svc.List(c.Request.Context(), c.Query("status"), page, pageSize)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, pd)
}

// Get GET /api/v1/articles/:id：文章详情（含配图元数据）。
//
//	@Summary 文章详情
//	@Tags 文章
//	@Security BearerAuth
//	@Produce json
//	@Param id path int true "文章 ID"
//	@Success 200 {object} resp.Envelope{data=service.ArticleDetail}
//	@Router /articles/{id} [get]
func (h *ArticleHandler) Get(c *gin.Context) {
	id, okID := httputil.PathUint(c, "id")
	if !okID {
		return
	}
	detail, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, detail)
}

// Update PUT /api/v1/articles/:id：编辑标题/摘要/正文/标签。
// 已发布文章保存后回到 draft，需重新发版。
//
//	@Summary 编辑文章
//	@Description 已发布文章保存后自动退回 draft，需重新发版。
//	@Tags 文章
//	@Security BearerAuth
//	@Accept json
//	@Produce json
//	@Param id path int true "文章 ID"
//	@Param body body updateArticleRequest true "标题/摘要/正文/标签"
//	@Success 200 {object} resp.Envelope{data=model.Article}
//	@Router /articles/{id} [put]
func (h *ArticleHandler) Update(c *gin.Context) {
	id, okID := httputil.PathUint(c, "id")
	if !okID {
		return
	}
	var req updateArticleRequest
	if !httputil.BindJSON(c, &req) {
		return
	}
	art, err := h.svc.Update(c.Request.Context(), id, req.Title, req.Summary, req.ContentMD, req.Tags)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, art)
}

// Publish POST /api/v1/articles/:id/publish：发版（draft → published）。
//
//	@Summary 发版文章
//	@Description draft → published；同时触发 Redis 前台缓存清理与 RAG 向量化（异步）。
//	@Tags 文章
//	@Security BearerAuth
//	@Produce json
//	@Param id path int true "文章 ID"
//	@Success 200 {object} resp.Envelope{data=model.Article}
//	@Router /articles/{id}/publish [post]
func (h *ArticleHandler) Publish(c *gin.Context) {
	id, okID := httputil.PathUint(c, "id")
	if !okID {
		return
	}
	art, err := h.svc.Publish(c.Request.Context(), id, c.GetUint(middleware.CtxUserID))
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, art)
}

// Offline POST /api/v1/articles/:id/offline：下线（published → draft）。
//
//	@Summary 下线文章
//	@Description published → draft；同时清理 Redis 前台缓存并删除该篇 RAG 向量。
//	@Tags 文章
//	@Security BearerAuth
//	@Produce json
//	@Param id path int true "文章 ID"
//	@Success 200 {object} resp.Envelope{data=model.Article}
//	@Router /articles/{id}/offline [post]
func (h *ArticleHandler) Offline(c *gin.Context) {
	id, okID := httputil.PathUint(c, "id")
	if !okID {
		return
	}
	art, err := h.svc.Offline(c.Request.Context(), id)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, art)
}

// RegenerateFigures POST /api/v1/articles/:id/regenerate-figures：重新生成该篇配图。
// 涉及 LLM 调用，耗时较长，前端需放宽请求超时。
//
//	@Summary 重新生成配图
//	@Tags 文章
//	@Security BearerAuth
//	@Produce json
//	@Param id path int true "文章 ID"
//	@Success 200 {object} resp.Envelope{data=service.ArticleDetail}
//	@Router /articles/{id}/regenerate-figures [post]
func (h *ArticleHandler) RegenerateFigures(c *gin.Context) {
	id, okID := httputil.PathUint(c, "id")
	if !okID {
		return
	}
	detail, err := h.svc.RegenerateFigures(c.Request.Context(), id)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, detail)
}

// ModifyFigure POST /api/v1/articles/:id/figures/:figureId/modify：按指令 AI 修改单张配图。
// 只产出预览（新标题/数据/渲染 SVG + 变更摘要），不落库；用户确认后调
// ApplyFigure 才更新文章。涉及 LLM 调用，耗时较长，前端需放宽请求超时；
// 失败时原图保持不变。
//
//	@Summary 按指令 AI 修改配图（返回预览，不落库）
//	@Tags 文章
//	@Security BearerAuth
//	@Accept json
//	@Produce json
//	@Param id path int true "文章 ID"
//	@Param figureId path int true "配图 ID（SvgAsset 主键）"
//	@Param body body modifyFigureRequest true "修改指令（必填，≤200 字）"
//	@Success 200 {object} resp.Envelope{data=service.FigurePreview}
//	@Router /articles/{id}/figures/{figureId}/modify [post]
func (h *ArticleHandler) ModifyFigure(c *gin.Context) {
	id, okID := httputil.PathUint(c, "id")
	if !okID {
		return
	}
	figureID, okFig := httputil.PathUint(c, "figureId")
	if !okFig {
		return
	}
	var req modifyFigureRequest
	if !httputil.BindJSON(c, &req) {
		return
	}
	preview, err := h.svc.ModifyFigure(c.Request.Context(), id, figureID, req.Instruction)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, preview)
}

// ApplyFigure POST /api/v1/articles/:id/figures/:figureId/apply：确认应用配图修改。
// body 为 modify 返回的预览候选版本（title + spec），后端重校验并渲染后落库。
//
//	@Summary 确认应用配图修改（落库）
//	@Tags 文章
//	@Security BearerAuth
//	@Accept json
//	@Produce json
//	@Param id path int true "文章 ID"
//	@Param figureId path int true "配图 ID（SvgAsset 主键）"
//	@Param body body applyFigureRequest true "预览返回的 title 与 spec"
//	@Success 200 {object} resp.Envelope{data=service.FigureMeta}
//	@Router /articles/{id}/figures/{figureId}/apply [post]
func (h *ArticleHandler) ApplyFigure(c *gin.Context) {
	id, okID := httputil.PathUint(c, "id")
	if !okID {
		return
	}
	figureID, okFig := httputil.PathUint(c, "figureId")
	if !okFig {
		return
	}
	var req applyFigureRequest
	if !httputil.BindJSON(c, &req) {
		return
	}
	meta, err := h.svc.ApplyFigureChange(c.Request.Context(), id, figureID, req.Title, req.Spec)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, meta)
}

// GetReviews GET /api/v1/articles/:id/reviews：文章的 LLM 评审记录列表（最新 10 条）。
//
//	@Summary 文章评审记录（管理端）
//	@Tags 文章
//	@Security BearerAuth
//	@Produce json
//	@Param id path int true "文章 ID"
//	@Success 200 {object} resp.Envelope{data=[]model.ArticleReview}
//	@Router /articles/{id}/reviews [get]
func (h *ArticleHandler) GetReviews(c *gin.Context) {
	id, okID := httputil.PathUint(c, "id")
	if !okID {
		return
	}
	reviews, err := h.svc.ListReviews(c.Request.Context(), id)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, reviews)
}

type modifyFigureRequest struct {
	Instruction string `json:"instruction" binding:"required"` // 修改指令（非空，长度校验在 service）
}

type applyFigureRequest struct {
	Title string `json:"title" binding:"required"` // 预览返回的新标题
	Spec  string `json:"spec" binding:"required"`  // 预览返回的新图数据 JSON 文本
}

type updateArticleRequest struct {
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	ContentMD string   `json:"content_md"`
	Tags      []string `json:"tags"`
}
