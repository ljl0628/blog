package model

import (
	"time"
)

// LlmScene LLM 调用场景：用量按场景聚合统计，值域受限。
type LlmScene string

const (
	LlmSceneAnalyze      LlmScene = "analyze"       // 仓库分析（pipeline 第一轮）
	LlmSceneWriteArticle LlmScene = "write_article" // 文章写作（pipeline 第二轮）
	LlmSceneRagChat      LlmScene = "rag_chat"      // RAG 问答（改写 + 流式回答）
	LlmSceneEmbedding    LlmScene = "embedding"     // 向量化（/embeddings）
	LlmSceneJudge        LlmScene = "judge"         // 质量评审（第三阶段使用）
	LlmSceneRefigure     LlmScene = "refigure"      // 管理端重新生成配图
)

// LlmUsage LLM 调用用量记录：每次调用一行，按场景统计 token 消耗与耗时。
type LlmUsage struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	Scene            LlmScene  `gorm:"size:32;index:idx_scene_created,priority:1" json:"scene"` // 调用场景
	Model            string    `gorm:"size:64" json:"model"`                                    // 调用的模型名
	PromptTokens     int       `json:"prompt_tokens"`                                           // 输入 token 数
	CompletionTokens int       `json:"completion_tokens"`                                       // 输出 token 数
	DurationMS       int       `json:"duration_ms"`                                             // 调用耗时（毫秒）
	Status           string    `gorm:"size:16" json:"status"`                                   // success / error
	Error            string    `gorm:"size:512" json:"error"`                                   // 失败时的错误摘要
	CreatedAt        time.Time `gorm:"index:idx_scene_created,priority:2" json:"created_at"`
}

func (LlmUsage) TableName() string { return "llm_usage" }
