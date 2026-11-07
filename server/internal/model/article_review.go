package model

import (
	"time"
)

// ReviewVerdict 文章评审结论：pass（通过）/ needs_review（需人工复核）。
type ReviewVerdict string

const (
	ReviewVerdictPass        ReviewVerdict = "pass"         // 通过
	ReviewVerdictNeedsReview ReviewVerdict = "needs_review" // 需人工复核
)

// ArticleReview 文章质量评审记录：LLM judge 按四个维度打分，落库供管理端审核参考。
type ArticleReview struct {
	ID               uint          `gorm:"primaryKey" json:"id"`
	ArticleID        uint          `gorm:"index" json:"article_id"`    // 关联文章
	ScoreTotal       float64       `json:"score_total"`                // 总分
	ScoreAccuracy    float64       `json:"score_accuracy"`             // 准确性 1-5
	ScoreStructure   float64       `json:"score_structure"`            // 结构性 1-5
	ScoreReadability float64       `json:"score_readability"`          // 可读性 1-5
	ScoreFigure      float64       `json:"score_figure"`               // 配图质量 1-5
	Verdict          ReviewVerdict `gorm:"size:16" json:"verdict"`     // 结论：pass / needs_review（总分 ≥3.5 为 pass）
	JudgeModel       string        `gorm:"size:64" json:"judge_model"` // 评审所用模型
	Comment          string        `gorm:"size:512" json:"comment"`    // 评审意见摘要
	CreatedAt        time.Time     `json:"created_at"`
}

func (ArticleReview) TableName() string { return "article_review" }
