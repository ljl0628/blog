package service

import (
	"math"
	"testing"

	"blog/server/internal/config"
)

// TestCostOf table-driven 验证成本纯函数：正常查表、大小写不敏感（viper 键小写化坑）、
// 未知模型返 0、空表（空 map / nil map）返 0、零 token 返 0。
func TestCostOf(t *testing.T) {
	// 键故意写成小写形态：模拟 viper 读 yaml 后 map 键被统一小写化的真实结构
	pricing := map[string]config.LLMPrice{
		"glm-4-flash": {Prompt: 0.001, Completion: 0.002},
		"baai/bge-m3": {Prompt: 0.0005, Completion: 0},
	}
	tests := []struct {
		name             string
		pricing          map[string]config.LLMPrice
		model            string
		promptTokens     int64
		completionTokens int64
		want             float64
	}{
		{
			name:             "正常查表：prompt 与 completion 分别计价",
			pricing:          pricing,
			model:            "glm-4-flash",
			promptTokens:     1000,
			completionTokens: 2000,
			want:             0.001*1 + 0.002*2,
		},
		{
			name:             "大小写不敏感：原始模型名 BAAI/bge-m3 命中小写键",
			pricing:          pricing,
			model:            "BAAI/bge-m3",
			promptTokens:     2000,
			completionTokens: 1000,
			want:             0.0005 * 2,
		},
		{
			name:             "未知模型返 0",
			pricing:          pricing,
			model:            "gpt-4o",
			promptTokens:     1000,
			completionTokens: 1000,
			want:             0,
		},
		{
			name:             "空表返 0（空 map）",
			pricing:          map[string]config.LLMPrice{},
			model:            "glm-4-flash",
			promptTokens:     1000,
			completionTokens: 1000,
			want:             0,
		},
		{
			name:             "空表返 0（nil map）",
			pricing:          nil,
			model:            "glm-4-flash",
			promptTokens:     1000,
			completionTokens: 1000,
			want:             0,
		},
		{
			name:             "零 token 返 0",
			pricing:          pricing,
			model:            "glm-4-flash",
			promptTokens:     0,
			completionTokens: 0,
			want:             0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := costOf(tt.pricing, tt.model, tt.promptTokens, tt.completionTokens)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("costOf(%q, %d, %d) = %v, want %v",
					tt.model, tt.promptTokens, tt.completionTokens, got, tt.want)
			}
		})
	}
}
