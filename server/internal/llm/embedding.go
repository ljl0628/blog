package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"blog/server/internal/config"
	"blog/server/internal/model"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Embedder OpenAI 兼容 /embeddings 端点客户端：文本向量化，供 RAG 检索使用。
// 与 Chat 客户端解耦——chat 可能走 Anthropic 协议，embedding 只有 OpenAI 形态。
// 端点文档：https://docs.siliconflow.cn/cn/api-reference（本项目用 SiliconFlow BAAI/bge-m3，免费档）
type Embedder struct {
	baseURL        string
	apiKey         string
	model          string
	dims           int
	sendDimensions bool
	http           *http.Client
	db             *gorm.DB // 可为 nil，跳过用量落库（与 Client.db 同模式）
	log            *zap.Logger
}

// NewEmbedder 创建 embedding 客户端；base_url/api_key 未单独配置时回落到 chat 配置；
// db 可为 nil（跳过用量落库）。
func NewEmbedder(cfg config.RAG, chat config.LLM, db *gorm.DB, log *zap.Logger) *Embedder {
	base := cfg.EmbeddingBaseURL
	if base == "" {
		base = chat.BaseURL
	}
	key := cfg.EmbeddingAPIKey
	if key == "" {
		key = chat.APIKey
	}
	return &Embedder{
		baseURL:        base,
		apiKey:         key,
		model:          cfg.EmbeddingModel,
		dims:           cfg.EmbeddingDimensions,
		sendDimensions: cfg.EmbeddingSendDimensions,
		http:           &http.Client{Timeout: 60 * time.Second},
		db:             db,
		log:            log,
	}
}

// embedRequest /embeddings 请求体；dimensions 用指针区分「未配置不发」与「发 0」，
// 部分 provider（如 SiliconFlow 的 bge-m3）不接受该参数。
type embedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions *int     `json:"dimensions,omitempty"`
}

// usageEmbed 是 /embeddings 响应中的 token 用量（只有输入侧计费，无 completion 概念）。
type usageEmbed struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// embedResponse /embeddings 响应体；data[i].index 标记与输入的对应关系。
type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Usage *usageEmbed `json:"usage"`
}

// Embed 批量向量化文本，返回向量顺序与输入一致；网络/HTTP 错误指数退避重试。
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, len(texts))
	var lastErr error
	for attempt := 0; attempt <= maxCallRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<uint(attempt-1)) * time.Second):
			}
		}
		// 埋点粒度 = 物理请求：每次 embedOnce（一次 HTTP 往返）各记一行用量，
		// 网络重试 N 次记 N 行（与 Client.chatWithRetry 同粒度）。
		start := time.Now()
		usage, err := e.embedOnce(ctx, texts, out)
		e.recordUsage(usage, start, err)
		if err == nil {
			return out, nil
		}
		e.log.Warn("embedding 调用失败，准备重试", zap.Int("attempt", attempt+1), zap.Error(err))
		lastErr = err
	}
	return nil, lastErr
}

// recordUsage 把一次 embedding 物理请求的用量落库（llm_usage 表），逻辑对齐
// Client.recordUsage（两者分属不同结构体，为控制改动面不做提取复用）：
// token 数来自响应 usage 字段（缺失记零值），callErr 非空时记为 error 行；
// db 为 nil 时直接返回；落库失败仅告警，不影响调用主流程。
// 记账不随调用方 ctx 取消而丢失（同 Client.recordUsage），内部用独立短超时 ctx。
func (e *Embedder) recordUsage(u usageInfo, start time.Time, callErr error) {
	wctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e.db == nil {
		return
	}
	rec := model.LlmUsage{
		Scene:            model.LlmSceneEmbedding,
		Model:            e.model,
		PromptTokens:     u.Prompt,
		CompletionTokens: u.Completion, // embedding 无输出 token，恒为 0
		DurationMS:       int(time.Since(start).Milliseconds()),
		Status:           usageStatusSuccess,
	}
	if callErr != nil {
		rec.Status = usageStatusError
		rec.Error = truncRunes(callErr.Error(), maxUsageErrorRunes)
	}
	if err := e.db.WithContext(wctx).Create(&rec).Error; err != nil {
		e.log.Warn("embedding 用量落库失败", zap.Error(err))
	}
}

// embedOnce 单次调用 /embeddings，按响应中的 index 回填结果。
// 返回的 usageInfo 来自响应 usage 字段（embedding 只有输入侧计费，
// 映射时 Completion 恒为 0），响应缺失该字段时为零值。
func (e *Embedder) embedOnce(ctx context.Context, texts []string, out [][]float32) (usageInfo, error) {
	var dims *int
	if e.sendDimensions {
		dims = &e.dims
	}
	payload, err := json.Marshal(embedRequest{
		Model:      e.model,
		Input:      texts,
		Dimensions: dims,
	})
	if err != nil {
		return usageInfo{}, err
	}
	url := strings.TrimSuffix(e.baseURL, "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return usageInfo{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.http.Do(req)
	if err != nil {
		return usageInfo{}, err
	}
	defer resp.Body.Close()

	var er embedResponse
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&er); err != nil {
		return usageInfo{}, fmt.Errorf("响应解析失败(HTTP %d): %w", resp.StatusCode, err)
	}
	var usage usageInfo // 响应不含 usage 字段时保持零值
	if er.Usage != nil {
		usage.Prompt = er.Usage.PromptTokens
	}
	if resp.StatusCode != http.StatusOK || er.Error != nil {
		msg := http.StatusText(resp.StatusCode)
		if er.Error != nil {
			msg = er.Error.Message
		}
		// 已拿到响应体说明 token 已被厂商计费，语义错误也带上已解析的用量
		return usage, fmt.Errorf("接口返回 HTTP %d: %s", resp.StatusCode, msg)
	}
	if len(er.Data) != len(texts) {
		return usage, fmt.Errorf("返回向量数 %d 与输入 %d 不一致", len(er.Data), len(texts))
	}
	for _, d := range er.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return usage, fmt.Errorf("非法向量下标 %d", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if len(v) == 0 {
			return usage, fmt.Errorf("第 %d 条文本未返回向量", i)
		}
	}
	return usage, nil
}
