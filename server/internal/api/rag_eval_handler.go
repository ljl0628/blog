package api

import (
	"strconv"

	"blog/server/internal/httputil"
	"blog/server/internal/resp"
	"blog/server/internal/service"

	"github.com/gin-gonic/gin"
)

// RagEvalHandler RAG 回归评测接口（admin）：评测集 CRUD、无命中问题回流补录、评测轮触发与查询。
type RagEvalHandler struct {
	svc *service.RagEvalService
}

// NewRagEvalHandler 创建评测 handler。
func NewRagEvalHandler(svc *service.RagEvalService) *RagEvalHandler {
	return &RagEvalHandler{svc: svc}
}

// ListCases GET /api/v1/rag-eval/cases：全量评测用例（含停用项，用例量小不分页）。
//
//	@Summary 评测用例列表（管理端）
//	@Tags RAG 评测
//	@Security BearerAuth
//	@Produce json
//	@Success 200 {object} resp.Envelope{data=[]model.RagEvalCase}
//	@Router /rag-eval/cases [get]
func (h *RagEvalHandler) ListCases(c *gin.Context) {
	cases, err := h.svc.ListCases(c.Request.Context())
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, cases)
}

// CreateCase POST /api/v1/rag-eval/cases：新增人工评测用例（source=manual）。
//
//	@Summary 新增评测用例
//	@Tags RAG 评测
//	@Security BearerAuth
//	@Accept json
//	@Produce json
//	@Param body body createEvalCaseRequest true "问题 + 期望命中文章 ID 集"
//	@Success 200 {object} resp.Envelope{data=model.RagEvalCase}
//	@Router /rag-eval/cases [post]
func (h *RagEvalHandler) CreateCase(c *gin.Context) {
	var req createEvalCaseRequest
	if !httputil.BindJSON(c, &req) {
		return
	}
	cs, err := h.svc.CreateCase(c.Request.Context(), req.Question, req.ExpectArticleIDs)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, cs)
}

// UpdateCase PUT /api/v1/rag-eval/cases/:id：更新用例的问题 / 期望文章集 / 启停状态。
//
//	@Summary 更新评测用例
//	@Tags RAG 评测
//	@Security BearerAuth
//	@Accept json
//	@Produce json
//	@Param id path int true "用例 ID"
//	@Param body body updateEvalCaseRequest true "问题 + 期望文章集 + 启停"
//	@Success 200 {object} resp.Envelope{data=model.RagEvalCase}
//	@Router /rag-eval/cases/{id} [put]
func (h *RagEvalHandler) UpdateCase(c *gin.Context) {
	id, okID := httputil.PathUint(c, "id")
	if !okID {
		return
	}
	var req updateEvalCaseRequest
	if !httputil.BindJSON(c, &req) {
		return
	}
	cs, err := h.svc.UpdateCase(c.Request.Context(), id, req.Question, req.ExpectArticleIDs, req.Enabled)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, cs)
}

// DeleteCase DELETE /api/v1/rag-eval/cases/:id：删除用例。
//
//	@Summary 删除评测用例
//	@Tags RAG 评测
//	@Security BearerAuth
//	@Produce json
//	@Param id path int true "用例 ID"
//	@Success 200 {object} resp.Envelope
//	@Router /rag-eval/cases/{id} [delete]
func (h *RagEvalHandler) DeleteCase(c *gin.Context) {
	id, okID := httputil.PathUint(c, "id")
	if !okID {
		return
	}
	if err := h.svc.DeleteCase(c.Request.Context(), id); err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, nil)
}

// ImportFromUnanswered POST /api/v1/rag-eval/cases/import-unanswered：无命中问题转评测用例。
// 问题原文带入，期望文章集由人工指定（选题回流的补录路径）。
//
//	@Summary 无命中问题导入评测集
//	@Tags RAG 评测
//	@Security BearerAuth
//	@Accept json
//	@Produce json
//	@Param body body importEvalCaseRequest true "无命中问题 ID + 期望命中文章 ID 集"
//	@Success 200 {object} resp.Envelope{data=model.RagEvalCase}
//	@Router /rag-eval/cases/import-unanswered [post]
func (h *RagEvalHandler) ImportFromUnanswered(c *gin.Context) {
	var req importEvalCaseRequest
	if !httputil.BindJSON(c, &req) {
		return
	}
	cs, err := h.svc.ImportFromUnanswered(c.Request.Context(), req.UnansweredID, req.ExpectArticleIDs)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, cs)
}

// RunEval POST /api/v1/rag-eval/run：触发一轮评测（后台执行，立即返回 running 轮供前端轮询）。
// 已有进行中的轮时 service 返回 ErrConflict，resp.FailWith 映射 409（与 tasks 接口一致）。
//
//	@Summary 触发评测轮
//	@Tags RAG 评测
//	@Security BearerAuth
//	@Produce json
//	@Success 200 {object} resp.Envelope{data=model.RagEvalRun}
//	@Router /rag-eval/run [post]
func (h *RagEvalHandler) RunEval(c *gin.Context) {
	run, err := h.svc.RunEval(c.Request.Context())
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, run)
}

// ListRuns GET /api/v1/rag-eval/runs?limit=：最近评测轮列表（id 倒序，只带汇总指标）。
// limit 可选：缺省/非正数由 service 兜底 20，上限 50。
//
//	@Summary 评测轮列表（管理端）
//	@Tags RAG 评测
//	@Security BearerAuth
//	@Produce json
//	@Param limit query int false "返回条数，默认 20，上限 50"
//	@Success 200 {object} resp.Envelope{data=[]model.RagEvalRun}
//	@Router /rag-eval/runs [get]
func (h *RagEvalHandler) ListRuns(c *gin.Context) {
	// 非数字按缺省处理（Atoi 失败得 0），负数等非法值交 service 钳制
	limit, _ := strconv.Atoi(c.Query("limit"))
	runs, err := h.svc.ListRuns(c.Request.Context(), limit)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, runs)
}

// GetRun GET /api/v1/rag-eval/runs/:id：单个评测轮详情（含逐条明细 detail）。
//
//	@Summary 评测轮详情
//	@Tags RAG 评测
//	@Security BearerAuth
//	@Produce json
//	@Param id path int true "评测轮 ID"
//	@Success 200 {object} resp.Envelope{data=model.RagEvalRun}
//	@Router /rag-eval/runs/{id} [get]
func (h *RagEvalHandler) GetRun(c *gin.Context) {
	id, okID := httputil.PathUint(c, "id")
	if !okID {
		return
	}
	run, err := h.svc.GetRun(c.Request.Context(), id)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, run)
}

type createEvalCaseRequest struct {
	Question         string `json:"question" binding:"required"` // 评测问题（非空，余下校验在 service）
	ExpectArticleIDs []uint `json:"expect_article_ids"`          // 期望命中的文章 ID 集（service 校验存在性）
}

type updateEvalCaseRequest struct {
	Question         string `json:"question" binding:"required"` // 评测问题（非空）
	ExpectArticleIDs []uint `json:"expect_article_ids"`          // 期望命中的文章 ID 集
	Enabled          bool   `json:"enabled"`                     // 是否参与评测
}

type importEvalCaseRequest struct {
	UnansweredID     uint   `json:"unanswered_id" binding:"required"` // 无命中问题 ID（非 0）
	ExpectArticleIDs []uint `json:"expect_article_ids"`               // 人工指定的期望文章 ID 集
}
