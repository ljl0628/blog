package api

import (
	"strconv"

	"blog/server/internal/resp"
	"blog/server/internal/service"

	"github.com/gin-gonic/gin"
)

// MetricsHandler LLM 用量看板接口（admin，只读）。
type MetricsHandler struct {
	svc *service.MetricsService
}

// NewMetricsHandler 创建指标看板 handler。
func NewMetricsHandler(svc *service.MetricsService) *MetricsHandler {
	return &MetricsHandler{svc: svc}
}

// Summary GET /api/v1/metrics/summary：用量总览（今日/本月成本 + 全量规模统计）。
//
//	@Summary 用量总览（管理端）
//	@Tags 度量看板
//	@Security BearerAuth
//	@Produce json
//	@Success 200 {object} resp.Envelope{data=service.MetricsSummary}
//	@Router /metrics/summary [get]
func (h *MetricsHandler) Summary(c *gin.Context) {
	sum, err := h.svc.Summary(c.Request.Context())
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, sum)
}

// Daily GET /api/v1/metrics/daily?days=：按日 × 场景聚合的用量行（日期倒序）。
// days 可选：缺省/非正数由 service 兜底 30，上限 90。
//
//	@Summary 每日用量（管理端）
//	@Tags 度量看板
//	@Security BearerAuth
//	@Produce json
//	@Param days query int false "最近天数，默认 30，上限 90"
//	@Success 200 {object} resp.Envelope{data=[]service.MetricsDailyRow}
//	@Router /metrics/daily [get]
func (h *MetricsHandler) Daily(c *gin.Context) {
	// 非数字按缺省处理（Atoi 失败得 0），负数等非法值交 service 钳制
	days, _ := strconv.Atoi(c.Query("days"))
	rows, err := h.svc.Daily(c.Request.Context(), days)
	if err != nil {
		resp.FailWith(c, err)
		return
	}
	resp.OK(c, rows)
}
