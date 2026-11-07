// Package llm 实现 LLM Chat 客户端：JSON 结构化输出校验、
// 失败自动重试（指数退避）与基于 Redis 的简易限流。
//
// "OpenAI 兼容" 指一套请求/响应的 JSON 格式，而非 OpenAI 公司的服务——
// 智谱、DeepSeek、SiliconFlow 等厂商都按这套格式开放端点，换厂商只需换 base_url。
// base_url 含 "/anthropic" 时切换到 Anthropic Messages 格式（见 client_anthropic.go）。
// 厂商文档：智谱 https://docs.bigmodel.cn ；SiliconFlow https://docs.siliconflow.cn/cn/api-reference
// 各端点拼接与当前配置总表见 docs/third-party-apis.md。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"blog/server/internal/config"
	"blog/server/internal/model"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	maxCallRetries    = 3 // 网络/接口错误重试次数
	maxOutputAttempts = 3 // JSON 校验失败重试次数

	usageStatusSuccess = "success" // llm_usage.status：调用成功
	usageStatusError   = "error"   // llm_usage.status：调用失败
)

// Client 是 LLM 服务客户端，支持 OpenAI 兼容与 Anthropic Messages 两种请求格式，
// 由 cfg.BaseURL 自动判别；无论哪种格式，请求都发往同一个厂商端点（如智谱）。
type Client struct {
	cfg  config.LLM
	http *http.Client
	rdb  *redis.Client // 可为 nil，跳过限流
	db   *gorm.DB      // 可为 nil，跳过用量落库（与 rdb 限流同模式）
	log  *zap.Logger
}

// NewClient 创建 LLM 客户端；rdb / db 可为 nil（分别跳过限流与用量落库）。
func NewClient(cfg config.LLM, rdb *redis.Client, db *gorm.DB, log *zap.Logger) *Client {
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
		},
		rdb: rdb,
		db:  db,
		log: log,
	}
}

// ModelName 返回客户端当前使用的模型名，供调用方记录实际模型（如 judge 落库）。
func (c *Client) ModelName() string { return c.cfg.Model }

// ChatJSON 发起一次对话并要求模型输出 JSON，解析到 out；
// validate 为nil 时仅做 JSON 解析校验，否则追加语义校验，
// 任一校验失败会把错误信息反馈给模型重试（最多 maxOutputAttempts 次）。
// scene 标识调用场景（analyze/write_article 等），用于用量按场景聚合。
func (c *Client) ChatJSON(ctx context.Context, scene model.LlmScene, system, user string, out any, validate func() error) error {
	messages := []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	var lastErr error
	for attempt := 0; attempt < maxOutputAttempts; attempt++ {
		if attempt > 0 {
			hint := "你上一次的输出存在问题，无法通过校验，错误信息：" + lastErr.Error() +
				"。请严格按要求重新输出，直接给出合法 JSON，不要包含任何解释、markdown 代码块或多余文本。"
			messages = append(messages,
				Message{Role: "assistant", Content: "好的。"},
				Message{Role: "user", Content: hint},
			)
		}
		content, _, err := c.chatWithRetry(ctx, scene, messages)
		if err != nil {
			return fmt.Errorf("调用大模型失败: %w", err)
		}
		raw, err := extractJSON(content)
		if err != nil {
			lastErr = err
			continue
		}
		if err := json.Unmarshal(raw, out); err != nil {
			lastErr = fmt.Errorf("JSON 解析失败: %w", err)
			continue
		}
		if validate != nil {
			if err := validate(); err != nil {
				lastErr = err
				continue
			}
		}
		return nil
	}
	return fmt.Errorf("模型输出 %d 次均未通过校验: %w", maxOutputAttempts, lastErr)
}

// chatWithRetry 处理网络/HTTP 层错误，指数退避重试。
// 埋点粒度 = 物理请求：循环内每次 chatOnce（即一次 HTTP 往返）都落一行用量，
// 网络重试 N 次记 N 行；返回值携带成功那次的 token 用量供上层使用。
func (c *Client) chatWithRetry(ctx context.Context, scene model.LlmScene, messages []Message) (string, usageInfo, error) {
	var lastErr error
	for attempt := 0; attempt <= maxCallRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", usageInfo{}, ctx.Err()
			case <-time.After(time.Duration(1<<uint(attempt-1)) * time.Second):
			}
		}
		if err := c.throttle(ctx); err != nil {
			return "", usageInfo{}, err
		}
		start := time.Now()
		content, usage, err := c.chatOnce(ctx, messages)
		c.recordUsage(scene, c.cfg.Model, usage, start, err) // 成功与失败各记一行
		if err == nil {
			return content, usage, nil
		}
		lastErr = err
		c.log.Warn("LLM 调用失败，准备重试", zap.Int("attempt", attempt+1), zap.Error(err))
	}
	return "", usageInfo{}, lastErr
}

// maxUsageErrorRunes 用量记录错误摘要的最大字符数，对齐 llm_usage.error 列宽。
const maxUsageErrorRunes = 512

// recordUsage 把一次 LLM 物理请求的用量落库（llm_usage 表）：
// token 数来自响应 usage 字段（缺失记零值），callErr 非空时记为 error 行。
// db 为 nil 时直接返回；落库失败仅告警，不影响调用主流程。
// 记账不随调用方连接中断：token 已被厂商计费，SSE 客户端断开等导致请求
// ctx 取消时也必须落库，故内部用独立短超时 ctx，不接收调用方 ctx。
func (c *Client) recordUsage(scene model.LlmScene, modelName string, u usageInfo, start time.Time, callErr error) {
	wctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if c.db == nil {
		return
	}
	rec := model.LlmUsage{
		Scene:            scene,
		Model:            modelName,
		PromptTokens:     u.Prompt,
		CompletionTokens: u.Completion,
		DurationMS:       int(time.Since(start).Milliseconds()),
		Status:           usageStatusSuccess,
	}
	if callErr != nil {
		rec.Status = usageStatusError
		rec.Error = truncRunes(callErr.Error(), maxUsageErrorRunes)
	}
	if err := c.db.WithContext(wctx).Create(&rec).Error; err != nil {
		c.log.Warn("LLM 用量落库失败", zap.String("scene", string(scene)), zap.Error(err))
	}
}

// truncRunes 按字符数截断字符串，防止超长错误信息超出数据库列宽。
func truncRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}

// chatOnceOpenAI 调用 OpenAI 兼容的 /chat/completions（默认协议）。
// "调用大模型 API" 本质就是一个 HTTP 请求，四要素在此凑齐：
// 地址 = base_url+/chat/completions；身份 = api_key 认证头；
// 内容 = {model,messages} JSON；发送 = c.http.Do（请求真正上网络的时刻）。
// 返回的 usageInfo 来自响应 usage 字段，响应缺失该字段时为零值。
func (c *Client) chatOnceOpenAI(ctx context.Context, messages []Message) (string, usageInfo, error) {
	reqBody := chatRequest{
		Model:          c.cfg.Model,
		Messages:       messages,
		Temperature:    c.cfg.Temperature,
		MaxTokens:      c.cfg.MaxTokens,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}
	// 序列化：Go 结构体 → JSON 文本（[]byte）。请求体此刻还是内存里的字节，
	// 响应回来时下方 json.NewDecoder 做反向翻译（文本 → 结构体），一进一出对称。
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", usageInfo{}, err
	}
	url := strings.TrimSuffix(c.cfg.BaseURL, "/") + "/chat/completions"
	// 把 ctx 附到请求上：Do 等响应期间内部会盯着 ctx.Done()，
	// 取消（如管理端取消任务）/停机/超时一发生，Do 立即返回 context.Canceled，
	// 请求被中断——打断的是"等待"，goroutine 拿着 error 自己继续走错误处理。
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", usageInfo{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.http.Do(req) // 此前都是本地打包，这一行才真正发往厂商服务器
	if err != nil {
		return "", usageInfo{}, err
	}
	defer resp.Body.Close()

	var usage usageInfo // 响应不含 usage 字段时保持零值
	var cr chatResponse
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&cr); err != nil {
		return "", usage, fmt.Errorf("响应解析失败(HTTP %d): %w", resp.StatusCode, err)
	}
	if cr.Usage != nil {
		usage = usageInfo{Prompt: cr.Usage.PromptTokens, Completion: cr.Usage.CompletionTokens}
	}
	if resp.StatusCode != http.StatusOK {
		msg := http.StatusText(resp.StatusCode)
		if cr.Error != nil {
			msg = cr.Error.Message
		}
		return "", usage, fmt.Errorf("接口返回 HTTP %d: %s", resp.StatusCode, msg)
	}
	if cr.Error != nil {
		return "", usage, fmt.Errorf("接口返回错误: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", usage, errors.New("响应中没有 choices")
	}
	content := cr.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return "", usage, errors.New("响应内容为空")
	}
	return content, usage, nil
}

// throttle 基于 Redis 固定窗口限流；rdb 为 nil 时直接放行。
func (c *Client) throttle(ctx context.Context) error {
	if c.rdb == nil || c.cfg.RatePerMinute <= 0 {
		return nil
	}
	key := fmt.Sprintf("llm:ratelimit:%d", time.Now().Unix()/60) // 当前秒/60, 取整。即当前第几分钟
	for {
		n, err := c.rdb.Incr(ctx, key).Result()
		if err != nil {
			return nil // 限流依赖不可用时放行，不阻塞主流程
		}
		if n == 1 {
			c.rdb.Expire(ctx, key, 2*time.Minute)
		}
		if n <= int64(c.cfg.RatePerMinute) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// extractJSON 从模型输出中提取 JSON 体（容忍 markdown 代码块包裹）。
func extractJSON(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return nil, errors.New("输出中未找到 JSON 对象")
	}
	return []byte(s[start : end+1]), nil
}
