package service

import (
	"encoding/json"
	"strings"
	"testing"

	"blog/server/internal/model"
)

// TestUnwrapFigureData 覆盖包装结构剥壳与不误剥的场景：
// LLM 偶发输出 {"flow":{"steps":[...]}}，顶层唯一键恰为 kind 名时须剥出内层。
func TestUnwrapFigureData(t *testing.T) {
	cases := []struct {
		name string
		kind model.FigureKind
		data string
		want string
	}{
		{"flow 多包一层 kind 键", model.FigureKindFlow,
			`{"flow":{"steps":[{"name":"a","desc":"b"}]}}`,
			`{"steps":[{"name":"a","desc":"b"}]}`},
		{"flow 正常顶层结构不剥", model.FigureKindFlow,
			`{"steps":[{"name":"a","desc":"b"}]}`,
			`{"steps":[{"name":"a","desc":"b"}]}`},
		{"唯一键不是 kind 名不剥", model.FigureKindFlow,
			`{"steps":{}}`, `{"steps":{}}`},
		{"多个顶层键不剥", model.FigureKindFlow,
			`{"steps":[],"extra":1}`, `{"steps":[],"extra":1}`},
		{"畸形 JSON 原样返回", model.FigureKindFlow, `{bad`, `{bad`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(unwrapFigureData(c.kind, []byte(c.data)))
			var g, w any
			if json.Unmarshal([]byte(got), &g) == nil && json.Unmarshal([]byte(c.want), &w) == nil {
				// JSON 语义等价即可（键序/空白不敏感）
				gj, _ := json.Marshal(g)
				wj, _ := json.Marshal(w)
				if string(gj) != string(wj) {
					t.Fatalf("unwrap = %s, want %s", got, c.want)
				}
				return
			}
			if got != c.want {
				t.Fatalf("unwrap = %s, want %s", got, c.want)
			}
		})
	}
}

// TestMissingPlaceholders 覆盖悬空占位符提取：已有配图跳过、重复去重、
// id 两端空白剔除（与前台 inlineFigures 的 key.trim() 口径一致）、无缺失返回 nil。
func TestMissingPlaceholders(t *testing.T) {
	assets := []model.SvgAsset{{Placeholder: "fig-1"}, {Placeholder: "cover"}}
	md := "a\n\n{{figure:fig-1}}\n\nb\n\n{{figure:fig-2}}\n\n{{figure: fig-3 }}\n\n{{figure:fig-2}}"
	got := missingPlaceholders(md, assets)
	want := []string{"fig-2", "fig-3"}
	if len(got) != len(want) {
		t.Fatalf("missing = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("missing = %v, want %v", got, want)
		}
	}
	if m := missingPlaceholders("仅 {{figure:fig-1}} 一处", assets); m != nil {
		t.Fatalf("无缺失应返回 nil, got %v", m)
	}
}

// TestThemeDirectRoute 覆盖纯配色指令直路由：信号词+色词（长词优先）命中，
// 内容类或含糊指令不命中（走 LLM 路径）。
func TestThemeDirectRoute(t *testing.T) {
	cases := []struct {
		name string
		ins  string
		want string
		ok   bool
	}{
		{"海洋蓝背景", "背景太暗了，换成海洋蓝背景", "ocean", true},
		{"蓝色背景", "换成蓝色背景", "ocean", true},
		{"浅色配色", "配色换成浅色", "light", true},
		{"深空蓝长词优先", "背景用深空蓝", "midnight", true},
		{"森林绿", "背景换成森林绿", "forest", true},
		{"内容指令不命中", "合并前两步", "", false},
		{"只有色词无信号词不命中", "改成蓝色", "", false},
		{"只有信号词无色词不命中", "背景调整一下", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := themeDirectRoute(c.ins)
			if ok != c.ok || got != c.want {
				t.Fatalf("themeDirectRoute(%q) = (%q,%v), want (%q,%v)", c.ins, got, ok, c.want, c.ok)
			}
		})
	}
}

// TestMergeThemeAndSummary theme 并入/剔除与「纯换色不虚报数据更新」。
func TestMergeThemeAndSummary(t *testing.T) {
	data := `{"steps":[{"name":"a","desc":"b"}]}`
	merged := string(mergeTheme([]byte(data), "ocean"))
	// 并入后 theme 生效且数据保留
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(merged), &m); err != nil {
		t.Fatal(err)
	}
	if string(m["theme"]) != `"ocean"` {
		t.Fatalf("theme = %s, want \"ocean\"", m["theme"])
	}
	if !sameDataIgnoreTheme(data, merged) {
		t.Fatal("merge 后数据主体不应变化（sameDataIgnoreTheme 视角）")
	}
	// 纯换 theme：摘要不应包含「图数据已更新」
	if sum := figureChangeSummary("t", "t", data, merged); !strings.Contains(sum, "配色切换为海洋蓝") || strings.Contains(sum, "图数据已更新") {
		t.Fatalf("纯换色摘要错误: %s", sum)
	}
	// 数据真变了：摘要须报数据更新
	changed := `{"steps":[{"name":"a","desc":"c"}],"theme":"ocean"}`
	if sum := figureChangeSummary("t", "t", data, changed); !strings.Contains(sum, "图数据已更新") {
		t.Fatalf("数据变化未上报: %s", sum)
	}
	// 全无变化：明确告知
	if sum := figureChangeSummary("t", "t", data, data); !strings.Contains(sum, "未对图产生变化") {
		t.Fatalf("无变化摘要错误: %s", sum)
	}
}
