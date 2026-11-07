// Package svggen 将结构化图表数据确定性渲染为风格统一的 SVG 文档。
package svggen

import (
	"encoding/json"
	"fmt"

	"blog/server/internal/model"
)

type renderFn func(title string, data string) (string, error)

var renderers = map[model.FigureKind]renderFn{
	model.FigureKindArchitecture: renderArchitecture,
	model.FigureKindFlow:         renderFlow,
	model.FigureKindCompare:      renderCompare,
	model.FigureKindTimeline:     renderTimeline,
}

// Render 按图类型分发渲染，data 为 LLM 配图数据（JSON 字符串）。
// data 可含可选 theme 字段（见 palette.go themes），控制整图配色背景。
func Render(kind model.FigureKind, title string, data string) (string, error) {
	fn := renderers[kind]
	if fn == nil {
		return "", fmt.Errorf("不支持的图类型: %s", kind)
	}
	var meta struct {
		Theme string `json:"theme"`
	}
	_ = json.Unmarshal([]byte(data), &meta) // 解析失败按默认主题，后续渲染器自会报错
	return renderWithTheme(meta.Theme, func() (string, error) {
		return fn(title, data)
	})
}

// HasContent 判断图数据是否含主体内容（各 kind 的核心条目数 ≥1）。
// 渲染器对空数据会画成只剩标题的空图——修改链路须在落库前用它拦下
// 「改空」结果，否则坏数据还会成为下一轮修改的基线。
func HasContent(kind model.FigureKind, data string) bool {
	switch kind {
	case model.FigureKindArchitecture:
		var in archInput
		return json.Unmarshal([]byte(data), &in) == nil && len(in.Layers) > 0
	case model.FigureKindFlow:
		var in flowInput
		return json.Unmarshal([]byte(data), &in) == nil && len(in.Steps) > 0
	case model.FigureKindCompare:
		var in compareInput
		return json.Unmarshal([]byte(data), &in) == nil &&
			len(in.Left.Points) > 0 && len(in.Right.Points) > 0
	case model.FigureKindTimeline:
		var in timelineInput
		return json.Unmarshal([]byte(data), &in) == nil && len(in.Items) > 0
	default:
		// cover 等无主体条目结构的类型不适用本校验
		return true
	}
}
