package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"blog/server/internal/config"
)

// TestUsageParse 验证三家协议响应体中 usage 字段的解析：
// OpenAI 兼容 chat（prompt_tokens/completion_tokens）、Anthropic Messages
// （input_tokens/output_tokens）、embeddings（prompt_tokens/total_tokens）；
// 另验证不含 usage 字段的响应 Usage 为 nil 且解析不报错。
func TestUsageParse(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		probe   func(t *testing.T, payload string)
	}{
		{
			name: "OpenAI 兼容 chat 响应解析 token 用量",
			payload: `{
				"id": "chatcmpl-abc123",
				"object": "chat.completion",
				"model": "glm-4-flash",
				"choices": [
					{
						"index": 0,
						"message": {"role": "assistant", "content": "你好"},
						"finish_reason": "stop"
					}
				],
				"usage": {"prompt_tokens": 12, "completion_tokens": 34, "total_tokens": 46}
			}`,
			probe: func(t *testing.T, payload string) {
				var r chatResponse
				if err := json.Unmarshal([]byte(payload), &r); err != nil {
					t.Fatalf("Unmarshal 失败: %v", err)
				}
				if r.Usage == nil {
					t.Fatal("Usage 未解析，为 nil")
				}
				if r.Usage.PromptTokens != 12 {
					t.Errorf("PromptTokens = %d, 期望 12", r.Usage.PromptTokens)
				}
				if r.Usage.CompletionTokens != 34 {
					t.Errorf("CompletionTokens = %d, 期望 34", r.Usage.CompletionTokens)
				}
			},
		},
		{
			name: "Anthropic Messages 响应解析 token 用量",
			payload: `{
				"id": "msg_01ABC",
				"type": "message",
				"role": "assistant",
				"model": "glm-4.6",
				"content": [{"type": "text", "text": "你好"}],
				"stop_reason": "end_turn",
				"usage": {"input_tokens": 10, "output_tokens": 20}
			}`,
			probe: func(t *testing.T, payload string) {
				var r anthropicResponse
				if err := json.Unmarshal([]byte(payload), &r); err != nil {
					t.Fatalf("Unmarshal 失败: %v", err)
				}
				if r.Usage == nil {
					t.Fatal("Usage 未解析，为 nil")
				}
				if r.Usage.InputTokens != 10 {
					t.Errorf("InputTokens = %d, 期望 10", r.Usage.InputTokens)
				}
				if r.Usage.OutputTokens != 20 {
					t.Errorf("OutputTokens = %d, 期望 20", r.Usage.OutputTokens)
				}
			},
		},
		{
			name: "embeddings 响应解析 token 用量",
			payload: `{
				"object": "list",
				"model": "BAAI/bge-m3",
				"data": [
					{"index": 0, "embedding": [0.0123, -0.0456, 0.0789]}
				],
				"usage": {"prompt_tokens": 5, "total_tokens": 5}
			}`,
			probe: func(t *testing.T, payload string) {
				var r embedResponse
				if err := json.Unmarshal([]byte(payload), &r); err != nil {
					t.Fatalf("Unmarshal 失败: %v", err)
				}
				if r.Usage == nil {
					t.Fatal("Usage 未解析，为 nil")
				}
				if r.Usage.PromptTokens != 5 {
					t.Errorf("PromptTokens = %d, 期望 5", r.Usage.PromptTokens)
				}
				if r.Usage.TotalTokens != 5 {
					t.Errorf("TotalTokens = %d, 期望 5", r.Usage.TotalTokens)
				}
			},
		},
		{
			name: "OpenAI 兼容响应不含 usage 时 Usage 为 nil 且不报错",
			payload: `{
				"choices": [
					{"message": {"role": "assistant", "content": "你好"}, "finish_reason": "stop"}
				]
			}`,
			probe: func(t *testing.T, payload string) {
				var r chatResponse
				if err := json.Unmarshal([]byte(payload), &r); err != nil {
					t.Fatalf("不含 usage 的响应 Unmarshal 失败: %v", err)
				}
				if r.Usage != nil {
					t.Errorf("Usage 应为 nil，实际为 %+v", r.Usage)
				}
			},
		},
		{
			name: "Anthropic 响应不含 usage 时 Usage 为 nil 且不报错",
			payload: `{
				"content": [{"type": "text", "text": "你好"}]
			}`,
			probe: func(t *testing.T, payload string) {
				var r anthropicResponse
				if err := json.Unmarshal([]byte(payload), &r); err != nil {
					t.Fatalf("不含 usage 的响应 Unmarshal 失败: %v", err)
				}
				if r.Usage != nil {
					t.Errorf("Usage 应为 nil，实际为 %+v", r.Usage)
				}
			},
		},
		{
			name: "embeddings 响应不含 usage 时 Usage 为 nil 且不报错",
			payload: `{
				"object": "list",
				"data": [{"index": 0, "embedding": [0.1, 0.2]}]
			}`,
			probe: func(t *testing.T, payload string) {
				var r embedResponse
				if err := json.Unmarshal([]byte(payload), &r); err != nil {
					t.Fatalf("不含 usage 的响应 Unmarshal 失败: %v", err)
				}
				if r.Usage != nil {
					t.Errorf("Usage 应为 nil，实际为 %+v", r.Usage)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.probe(t, tc.payload)
		})
	}
}

// newSSEServer 起一个逐帧返回 SSE 数据的测试服务器，并在 reqProbe 中
// 暴露收到的请求体供断言（可为 nil）。
func newSSEServer(t *testing.T, frames []string, reqProbe func(t *testing.T, body map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reqProbe != nil {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("测试服务器解析请求体失败: %v", err)
			} else {
				reqProbe(t, body)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

// TestStreamUsageParse 验证两条流式路径的 token 用量解析与传递：
// OpenAI 兼容末帧 usage（需请求 stream_options.include_usage）、
// Anthropic message_start/message_delta 事件；帧不含 usage 时记零值不报错。
func TestStreamUsageParse(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "你好"}}

	t.Run("OpenAI 兼容流式末帧携带 usage", func(t *testing.T) {
		srv := newSSEServer(t, []string{
			`{"choices":[{"delta":{"content":"你"}}]}`,
			`{"choices":[{"delta":{"content":"好"}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34}}`,
		}, func(t *testing.T, body map[string]any) {
			opts, ok := body["stream_options"].(map[string]any)
			if !ok || opts["include_usage"] != true {
				t.Errorf("请求体 stream_options = %+v, 期望 include_usage=true", body["stream_options"])
			}
		})
		defer srv.Close()
		c := &Client{cfg: config.LLM{BaseURL: srv.URL, APIKey: "test", Model: "glm-4-flash"}, http: srv.Client()}

		var sb strings.Builder
		usage, err := c.streamOpenAI(context.Background(), msgs, func(s string) { sb.WriteString(s) })
		if err != nil {
			t.Fatalf("streamOpenAI 失败: %v", err)
		}
		if sb.String() != "你好" {
			t.Errorf("增量拼接 = %q, 期望 你好", sb.String())
		}
		if usage.Prompt != 12 || usage.Completion != 34 {
			t.Errorf("usage = %+v, 期望 {Prompt:12 Completion:34}", usage)
		}
	})

	t.Run("OpenAI 兼容流式不含 usage 记零值", func(t *testing.T) {
		srv := newSSEServer(t, []string{
			`{"choices":[{"delta":{"content":"你好"}}]}`,
		}, nil)
		defer srv.Close()
		c := &Client{cfg: config.LLM{BaseURL: srv.URL, APIKey: "test", Model: "glm-4-flash"}, http: srv.Client()}

		usage, err := c.streamOpenAI(context.Background(), msgs, func(string) {})
		if err != nil {
			t.Fatalf("不含 usage 的流不应报错: %v", err)
		}
		if usage != (usageInfo{}) {
			t.Errorf("usage = %+v, 期望零值", usage)
		}
	})

	t.Run("Anthropic 流式事件携带 usage", func(t *testing.T) {
		srv := newSSEServer(t, []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
			`{"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}`,
			`{"type":"message_stop"}`,
		}, nil)
		defer srv.Close()
		c := &Client{cfg: config.LLM{BaseURL: srv.URL, APIKey: "test", Model: "glm-4.6"}, http: srv.Client()}

		var sb strings.Builder
		usage, err := c.streamAnthropic(context.Background(), msgs, func(s string) { sb.WriteString(s) })
		if err != nil {
			t.Fatalf("streamAnthropic 失败: %v", err)
		}
		if sb.String() != "你好" {
			t.Errorf("增量拼接 = %q, 期望 你好", sb.String())
		}
		if usage.Prompt != 10 || usage.Completion != 20 {
			t.Errorf("usage = %+v, 期望 {Prompt:10 Completion:20}", usage)
		}
	})
}
