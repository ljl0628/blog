package model

import (
	"time"
)

// RagEvalSource 评测用例来源：人工录入 / 无命中问答回流沉淀。
type RagEvalSource string

const (
	RagEvalSourceManual     RagEvalSource = "manual"     // 人工录入
	RagEvalSourceUnanswered RagEvalSource = "unanswered" // 问答无命中回流沉淀
)

// RagEvalCase RAG 检索评测用例：一个问题 + 期望命中的文章集合，跑评测的基本单元。
type RagEvalCase struct {
	ID               uint          `gorm:"primaryKey" json:"id"`
	Question         string        `gorm:"size:512" json:"question"`                       // 评测问题
	ExpectArticleIDs JSON          `json:"expect_article_ids" swaggertype:"array,integer"` // 期望命中的文章 ID 数组
	Source           RagEvalSource `gorm:"size:16" json:"source"`                          // 用例来源
	Enabled          bool          `gorm:"not null;default:true" json:"enabled"`           // 是否参与评测
	CreatedAt        time.Time     `json:"created_at"`
}

func (RagEvalCase) TableName() string { return "rag_eval_case" }

// RagEvalRunStatus 评测运行状态：running（进行中）→ done（完成）/ failed（失败）。
type RagEvalRunStatus string

const (
	RagEvalRunStatusRunning RagEvalRunStatus = "running" // 进行中
	RagEvalRunStatusDone    RagEvalRunStatus = "done"    // 完成
	RagEvalRunStatusFailed  RagEvalRunStatus = "failed"  // 失败
)

// RagEvalRun 一次 RAG 评测运行的聚合结果：全量用例跑完后的命中率等指标。
type RagEvalRun struct {
	ID             uint             `gorm:"primaryKey" json:"id"`
	Status         RagEvalRunStatus `gorm:"size:16" json:"status"` // 运行状态
	Total          int              `json:"total"`                 // 参与评测的用例总数
	HitCount       int              `json:"hit_count"`             // 期望文章命中检索结果的用例数
	HitRate        float64          `json:"hit_rate"`              // 命中率（0-1）
	CitationOkRate float64          `json:"citation_ok_rate"`      // 引用正确率（0-1）
	AvgJudgeScore  *float64         `json:"avg_judge_score"`       // LLM judge 平均分，judge 关闭时为 null
	Detail         JSON             `json:"detail"`                // 逐条评测结果
	Error          string           `gorm:"size:512" json:"error"` // 失败原因摘要
	CreatedAt      time.Time        `json:"created_at"`
}

func (RagEvalRun) TableName() string { return "rag_eval_run" }
