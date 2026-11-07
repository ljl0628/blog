package llm

import "encoding/json"

// Message 是 OpenAI 兼容接口的对话消息。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    float32         `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

// usageOpenAI 是 OpenAI 兼容协议响应中的 token 用量。
type usageOpenAI struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
	Usage *usageOpenAI `json:"usage"`
}

// anthropicResponse 是 Anthropic Messages 协议的响应体，
// content 为分块数组，仅拼接 type=text 的文本块（忽略 thinking 等块）。
type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
	Usage *usageAnthropic `json:"usage"`
}

// usageAnthropic 是 Anthropic Messages 协议响应中的 token 用量。
type usageAnthropic struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// usageInfo 是一次 LLM 调用的 token 用量，供包内跨函数传递；
// embedding 场景只有输入侧计费，Completion 恒为 0。
type usageInfo struct {
	Prompt     int
	Completion int
}

// FigureOut 是 LLM 写作时输出的单张配图描述。
type FigureOut struct {
	ID    string          `json:"id"`   // 正文占位符标识，如 fig-1
	Kind  string          `json:"kind"` // architecture / flow / compare / timeline
	Title string          `json:"title"`
	Data  json.RawMessage `json:"data"` // 具体结构由 svggen 按 kind 解析
}

// ArticleOut 是 LLM 单篇写作的结构化输出。
type ArticleOut struct {
	Title    string      `json:"title"`
	Slug     string      `json:"slug"`
	Summary  string      `json:"summary"`
	Tags     []string    `json:"tags"`
	Markdown string      `json:"markdown"`
	Figures  []FigureOut `json:"figures"`
}

// ArticlePlanOut 是仓库分析输出中的单篇文章选题。
type ArticlePlanOut struct {
	Title       string   `json:"title"`
	Slug        string   `json:"slug"`
	Summary     string   `json:"summary"`
	Tags        []string `json:"tags"`
	Outline     []string `json:"outline"`
	TargetWords int      `json:"target_words"`
}

// AnalysisOut 是仓库分析阶段的结构化输出。
type AnalysisOut struct {
	RepoName    string           `json:"repo_name"`
	TechStack   []string         `json:"tech_stack"`
	Summary     string           `json:"summary"`
	Highlights  []string         `json:"highlights"`
	ArticlePlan []ArticlePlanOut `json:"article_plan"`
}

// FiguresOut 是配图重生成阶段的结构化输出。
type FiguresOut struct {
	Figures []FigureOut `json:"figures"`
}

// FigureModifyOut 是单张配图按指令 AI 修改阶段的结构化输出
//（目标图的 id/kind 已知，只产出新标题与数据）。
// Theme 为可选顶层字段（midnight/ocean/light/forest）：纯配色指令
// 由后端直路由处理，不走 LLM；此处仅兜底「内容+配色」复合指令。
type FigureModifyOut struct {
	Title string          `json:"title"`
	Theme string          `json:"theme,omitempty"`
	Data  json.RawMessage `json:"data"`
}

// JudgeOut 是 LLM 质量评审（judge）的结构化输出。
type JudgeOut struct {
	ScoreAccuracy    float64 `json:"score_accuracy"`    // 准确性 1~5
	ScoreStructure   float64 `json:"score_structure"`   // 结构性 1~5
	ScoreReadability float64 `json:"score_readability"` // 可读性 1~5
	ScoreFigure      float64 `json:"score_figure"`      // 配图质量 1~5
	ScoreTotal       float64 `json:"score_total"`       // 总分 = 四维平均，保留一位小数
	Verdict          string  `json:"verdict"`           // pass / needs_review
	Comment          string  `json:"comment"`           // 一句中文评语
}
