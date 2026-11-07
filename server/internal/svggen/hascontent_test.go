package svggen

import (
	"testing"

	"blog/server/internal/model"
)

// TestHasContent 覆盖各 kind 的正常/空/畸形数据判定：
// 空主体条目必须判 false（修改链路靠它拦「改空」结果），畸形 JSON 同样 false。
func TestHasContent(t *testing.T) {
	cases := []struct {
		name string
		kind string
		data string
		want bool
	}{
		{"flow 正常", "flow", `{"steps":[{"name":"a","desc":"b"}]}`, true},
		{"flow steps 为空数组", "flow", `{"steps":[]}`, false},
		{"flow 缺 steps 字段", "flow", `{}`, false},
		{"architecture 正常", "architecture", `{"layers":[{"name":"L","nodes":["n"]}]}`, true},
		{"architecture layers 空", "architecture", `{"layers":[]}`, false},
		{"compare 正常", "compare", `{"left":{"title":"A","points":["x"]},"right":{"title":"B","points":["y"]}}`, true},
		{"compare 左侧空", "compare", `{"left":{"title":"A","points":[]},"right":{"title":"B","points":["y"]}}`, false},
		{"timeline 正常", "timeline", `{"items":[{"time":"v1","title":"t","desc":"d"}]}`, true},
		{"timeline items 空", "timeline", `{"items":[]}`, false},
		{"flow 畸形 JSON", "flow", `{not json`, false},
		{"cover 不校验恒真", "cover", `{}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HasContent(model.FigureKind(c.kind), c.data); got != c.want {
				t.Fatalf("HasContent(%s) = %v, want %v", c.kind, got, c.want)
			}
		})
	}
}
