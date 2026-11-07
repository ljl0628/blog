package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"blog/server/internal/config"
	"blog/server/internal/model"

	"gorm.io/gorm"
)

const (
	metricsDailyDefaultDays = 30 // Daily 天数缺省值（入参 days<=0 时取）
	metricsDailyMaxDays     = 90 // Daily 天数上限，防大范围全表聚合
)

// MetricsService LLM 用量聚合查询与成本估算，作为管理端看板数据源。
// 成本不在 SQL 计算（价目表在内存）：一律先按模型分组聚合 token，再 Go 内查表求和。
type MetricsService struct {
	db      *gorm.DB
	pricing map[string]config.LLMPrice // 模型价目表（元/1k token），可为 nil（成本按 0）
}

// NewMetricsService 创建指标服务。pricing 传 cfg.LLM.Pricing，可为 nil/空。
func NewMetricsService(db *gorm.DB, pricing map[string]config.LLMPrice) *MetricsService {
	return &MetricsService{db: db, pricing: pricing}
}

// MetricsSummary 用量总览：今日/本月成本 + 全量调用规模统计。
type MetricsSummary struct {
	TodayCost        float64 `json:"today_cost"`        // 今日成本（元）
	MonthCost        float64 `json:"month_cost"`        // 本月成本（元）
	PromptTokens     int64   `json:"prompt_tokens"`     // 全量输入 token
	CompletionTokens int64   `json:"completion_tokens"` // 全量输出 token
	TotalTokens      int64   `json:"total_tokens"`      // prompt + completion
	Calls            int64   `json:"calls"`             // 调用总数
	AvgDurationMS    int64   `json:"avg_duration_ms"`   // 平均耗时（毫秒，四舍五入）
	ErrorRate        float64 `json:"error_rate"`        // 失败率（0~1，error 行占比；无数据为 0）
}

// MetricsDailyRow 按日 × 场景聚合的用量行（Daily 返回，日期倒序、场景升序）。
type MetricsDailyRow struct {
	Date             string  `json:"date"`              // 日期 YYYY-MM-DD（本地时区）
	Scene            string  `json:"scene"`             // 调用场景
	Calls            int64   `json:"calls"`             // 调用数
	PromptTokens     int64   `json:"prompt_tokens"`     // 输入 token 和
	CompletionTokens int64   `json:"completion_tokens"` // 输出 token 和
	AvgDurationMS    int64   `json:"avg_duration_ms"`   // 平均耗时（毫秒）
	Cost             float64 `json:"cost"`              // 该组成本（元）
}

// modelTokenRow 按模型分组的 token 聚合中间行（成本在 Go 内查表求和）。
type modelTokenRow struct {
	Model            string `gorm:"column:model"`
	PromptTokens     int64  `gorm:"column:prompt_tokens"`
	CompletionTokens int64  `gorm:"column:completion_tokens"`
}

// dailyAggRow 每日聚合的中间行：day × scene × model 粒度，Go 内合并为 MetricsDailyRow。
type dailyAggRow struct {
	Day              string `gorm:"column:day"`
	Scene            string `gorm:"column:scene"`
	Model            string `gorm:"column:model"`
	Calls            int64  `gorm:"column:calls"`
	PromptTokens     int64  `gorm:"column:prompt_tokens"`
	CompletionTokens int64  `gorm:"column:completion_tokens"`
	DurationMS       int64  `gorm:"column:duration_ms"` // SUM(duration_ms)，合并后除以 calls 得均值
}

// costOf 成本纯函数（元）：prompt/completion token 按价目表（元/1k token）分别计价求和。
//
// ⚠️ 已知坑：viper 读 yaml 会把 map 键统一小写化——yaml 写 BAAI/bge-m3，
// cfg.LLM.Pricing 里的实际键是 baai/bge-m3；而 llm_usage.Model 列存的是原始模型名
// （BAAI/bge-m3）。因此查表前必须对模型名 ToLower，否则 embedding 成本会静默记 0。
// 表无此模型或空表（含 nil map）返回 0，不 panic。
func costOf(pricing map[string]config.LLMPrice, model string, promptTokens, completionTokens int64) float64 {
	// 参数 model 遮蔽了 model 包名；本函数只做查表计价，不使用 model 包
	price, ok := pricing[strings.ToLower(model)]
	if !ok {
		return 0
	}
	return float64(promptTokens)/1000*price.Prompt + float64(completionTokens)/1000*price.Completion
}

// Summary 汇总用量总览：今日/本月成本各按模型分组聚合后查表求和，规模统计单独一趟全量聚合。
func (s *MetricsService) Summary(ctx context.Context) (MetricsSummary, error) {
	var sum MetricsSummary
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	// 成本：今日/本月各一趟 GROUP BY model（DSN loc=Local，边界按本地时区算即可对齐）
	todayRows, err := s.tokenRowsSince(ctx, todayStart)
	if err != nil {
		return sum, fmt.Errorf("聚合今日用量: %w", err)
	}
	monthRows, err := s.tokenRowsSince(ctx, monthStart)
	if err != nil {
		return sum, fmt.Errorf("聚合本月用量: %w", err)
	}
	for _, r := range todayRows {
		sum.TodayCost += costOf(s.pricing, r.Model, r.PromptTokens, r.CompletionTokens)
	}
	for _, r := range monthRows {
		sum.MonthCost += costOf(s.pricing, r.Model, r.PromptTokens, r.CompletionTokens)
	}

	// 规模统计：全量一趟聚合。无 GROUP BY 恒返回一行，但空表时 SUM/AVG 为 NULL，须 COALESCE 兜底
	var tot struct {
		Calls            int64   `gorm:"column:calls"`
		PromptTokens     int64   `gorm:"column:prompt_tokens"`
		CompletionTokens int64   `gorm:"column:completion_tokens"`
		AvgDurationMS    float64 `gorm:"column:avg_duration_ms"`
		ErrorCalls       int64   `gorm:"column:error_calls"`
	}
	err = s.db.WithContext(ctx).Model(&model.LlmUsage{}).
		Select(`COUNT(*) AS calls,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(AVG(duration_ms), 0) AS avg_duration_ms,
			COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0) AS error_calls`).
		Scan(&tot).Error
	if err != nil {
		return sum, fmt.Errorf("聚合全量用量: %w", err)
	}

	sum.PromptTokens = tot.PromptTokens
	sum.CompletionTokens = tot.CompletionTokens
	sum.TotalTokens = tot.PromptTokens + tot.CompletionTokens
	sum.Calls = tot.Calls
	sum.AvgDurationMS = int64(math.Round(tot.AvgDurationMS))
	if tot.Calls > 0 {
		sum.ErrorRate = float64(tot.ErrorCalls) / float64(tot.Calls)
	}
	return sum, nil
}

// Daily 最近 days 天（含今日）按日 × 场景聚合的用量行；days<=0 取 30，上限 90。
// model 一并进 GROUP BY（一次查询，避免逐行再查的 N+1），Go 内按 (date, scene) 合并并查表算成本。
func (s *MetricsService) Daily(ctx context.Context, days int) ([]MetricsDailyRow, error) {
	if days <= 0 {
		days = metricsDailyDefaultDays
	}
	if days > metricsDailyMaxDays {
		days = metricsDailyMaxDays
	}
	now := time.Now()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, -(days - 1))

	var rows []dailyAggRow
	err := s.db.WithContext(ctx).Model(&model.LlmUsage{}).
		Select(`DATE_FORMAT(created_at, '%Y-%m-%d') AS day, scene, model,
			COUNT(*) AS calls,
			SUM(prompt_tokens) AS prompt_tokens,
			SUM(completion_tokens) AS completion_tokens,
			SUM(duration_ms) AS duration_ms`).
		Where("created_at >= ?", since).
		Group("DATE_FORMAT(created_at, '%Y-%m-%d'), scene, model").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("聚合每日用量: %w", err)
	}

	// Go 内合并同 (day, scene) 的多模型行；key 用 "\x00" 拼接保证无歧义
	idx := make(map[string]int, len(rows))
	durations := make(map[string]int64, len(rows))
	out := make([]MetricsDailyRow, 0, len(rows))
	for _, r := range rows {
		key := r.Day + "\x00" + r.Scene
		i, ok := idx[key]
		if !ok {
			out = append(out, MetricsDailyRow{Date: r.Day, Scene: r.Scene})
			i = len(out) - 1
			idx[key] = i
		}
		row := &out[i]
		row.Calls += r.Calls
		row.PromptTokens += r.PromptTokens
		row.CompletionTokens += r.CompletionTokens
		row.Cost += costOf(s.pricing, r.Model, r.PromptTokens, r.CompletionTokens)
		durations[key] += r.DurationMS
	}
	for key, i := range idx {
		out[i].AvgDurationMS = durations[key] / out[i].Calls // 每组至少一行，Calls >= 1 不会除零
	}

	// 日期字符串 YYYY-MM-DD 字典序即时序，倒序即最近在前
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date > out[j].Date
		}
		return out[i].Scene < out[j].Scene
	})
	return out, nil
}

// tokenRowsSince 查询 created_at >= since 的按模型分组 token 聚合行。
func (s *MetricsService) tokenRowsSince(ctx context.Context, since time.Time) ([]modelTokenRow, error) {
	var rows []modelTokenRow
	err := s.db.WithContext(ctx).Model(&model.LlmUsage{}).
		Select("model, SUM(prompt_tokens) AS prompt_tokens, SUM(completion_tokens) AS completion_tokens").
		Where("created_at >= ?", since).
		Group("model").
		Scan(&rows).Error
	return rows, err
}
