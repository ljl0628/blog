package svggen

import (
	"fmt"
	"strings"
	"sync"
)

// palette 一套图表配色的全部色槽；渲染器只引用色槽变量，不感知主题。
type palette struct {
	BG, BG2, Panel, Panel2, Border string
	Accent, Accent2, Accent3       string
	Text, Muted                    string
}

// themes 预设主题，key 为 spec data 里的可选 theme 字段值。
// 配色约束：背景与面板同色系分层、文字对比度足、三强调色分工不变
// （Accent 主强调 / Accent2 次强调 / Accent3 成功态）。
var themes = map[string]palette{
	// midnight 深空蓝（历史默认，存量 spec 无 theme 字段时使用）
	"midnight": {
		BG: "#0F172A", BG2: "#1E293B", Panel: "#16213A", Panel2: "#1C2A47", Border: "#2E3F5C",
		Accent: "#38BDF8", Accent2: "#A78BFA", Accent3: "#34D399", Text: "#E2E8F0", Muted: "#94A3B8",
	},
	// ocean 海洋蓝：整体提亮的蓝调背景
	"ocean": {
		BG: "#0C4A6E", BG2: "#075985", Panel: "#0E5387", Panel2: "#11589A", Border: "#2A6FA3",
		Accent: "#7DD3FC", Accent2: "#C4B5FD", Accent3: "#6EE7B7", Text: "#E0F2FE", Muted: "#A5CDE8",
	},
	// light 浅色：浅底深字
	"light": {
		BG: "#F8FAFC", BG2: "#EEF2F7", Panel: "#FFFFFF", Panel2: "#F1F5F9", Border: "#CBD5E1",
		Accent: "#0284C7", Accent2: "#7C3AED", Accent3: "#059669", Text: "#1E293B", Muted: "#64748B",
	},
	// forest 森林绿
	"forest": {
		BG: "#06281E", BG2: "#0C3B2C", Panel: "#0A3327", Panel2: "#0E4030", Border: "#1D5C46",
		Accent: "#34D399", Accent2: "#FBBF24", Accent3: "#93C5FD", Text: "#D1FAE5", Muted: "#8CC0AA",
	},
}

// defaultThemeName 默认主题（存量 spec 无 theme 字段时的历史深色）。
const defaultThemeName = "midnight"

// 画布宽度。
const baseW = 1200

// 色槽变量：随主题切换重赋值，须在 renderMu 内访问。
var (
	renderMu sync.Mutex // 主题为包级状态，渲染全程持锁防并发互踩
	cBG      = themes[defaultThemeName].BG
	cBG2     = themes[defaultThemeName].BG2
	cPanel   = themes[defaultThemeName].Panel
	cPanel2  = themes[defaultThemeName].Panel2
	cBorder  = themes[defaultThemeName].Border
	cAccent  = themes[defaultThemeName].Accent
	cAccent2 = themes[defaultThemeName].Accent2
	cAccent3 = themes[defaultThemeName].Accent3
	cText    = themes[defaultThemeName].Text
	cMuted   = themes[defaultThemeName].Muted
)

// applyPalette 重设全部色槽变量（须持 renderMu）。
func applyPalette(p palette) {
	cBG, cBG2, cPanel, cPanel2, cBorder = p.BG, p.BG2, p.Panel, p.Panel2, p.Border
	cAccent, cAccent2, cAccent3 = p.Accent, p.Accent2, p.Accent3
	cText, cMuted = p.Text, p.Muted
}

// IsValidTheme 判断是否为已知预设主题名（LLM 输出的 theme 落库前校验用）。
func IsValidTheme(name string) bool {
	_, ok := themes[name]
	return ok
}

// renderWithTheme 在指定主题下执行渲染；未知主题回退默认。
func renderWithTheme(name string, fn func() (string, error)) (string, error) {
	renderMu.Lock()
	defer renderMu.Unlock()
	p, ok := themes[name]
	if !ok {
		p = themes[defaultThemeName]
	}
	applyPalette(p)
	return fn()
}

// fontFamily SVG 文本字体。
const fontFamily = `'Segoe UI','PingFang SC','Microsoft YaHei',sans-serif`

// esc 转义 XML 特殊字符。
func esc(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return r.Replace(s)
}

// textW 估算文本渲染宽度：CJK≈字号，拉丁≈0.62×字号。
func textW(s string, size float64) float64 {
	w := 0.0
	for _, r := range s {
		switch {
		case r >= 0x2E80:
			w += size
		case r == ' ':
			w += size * 0.30
		default:
			w += size * 0.62
		}
	}
	return w
}

// wrapText 按显示宽度断行（中文按字、拉丁按词）。
func wrapText(s string, maxW float64, size float64) []string {
	var lines []string
	cur := ""
	curW := 0.0
	flush := func() {
		lines = append(lines, cur)
		cur = ""
		curW = 0
	}
	var word string
	pushWord := func(word string) {
		ww := textW(word, size)
		spaceW := textW(" ", size)
		if cur == "" {
			cur, curW = word, ww
			return
		}
		if curW+spaceW+ww <= maxW {
			cur += " " + word
			curW += spaceW + ww
			return
		}
		flush()
		cur, curW = word, ww
	}
	for _, r := range s {
		if r >= 0x2E80 { // CJK 逐字断行
			cw := size
			if curW+cw > maxW {
				flush()
			}
			cur += string(r)
			curW += cw
			continue
		}
		if r == ' ' || r == '\n' {
			if word != "" {
				pushWord(word)
				word = ""
			}
			if r == '\n' {
				flush()
			}
			continue
		}
		word += string(r)
	}
	if word != "" {
		pushWord(word)
	}
	if cur != "" {
		flush()
	}
	return lines
}

// rectEl 圆角矩形元素。
func rectEl(x, y, w, h, rx float64, fill, stroke string) string {
	strokeAttr := ""
	if stroke != "" {
		strokeAttr = fmt.Sprintf(` stroke="%s"`, stroke)
	}
	return fmt.Sprintf(
		`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="%.1f" fill="%s"%s/>`,
		x, y, w, h, rx, fill, strokeAttr)
}

// textEl 文本元素（anchor: start/middle/end）。
func textEl(x, y float64, s string, size float64, fill, weight, anchor string) string {
	return fmt.Sprintf(
		`<text x="%.1f" y="%.1f" font-size="%.0f" fill="%s" font-weight="%s" text-anchor="%s">%s</text>`,
		x, y, size, fill, weight, anchor, esc(s))
}

// lineEl 直线/连线元素。
func lineEl(x1, y1, x2, y2 float64, stroke string, width float64, dashed bool) string {
	dash := ""
	if dashed {
		dash = ` stroke-dasharray="6 5"`
	}
	return fmt.Sprintf(
		`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="%.1f"%s/>`,
		x1, y1, x2, y2, stroke, width, dash)
}

// svgOpen 生成 SVG 头部（含渐变与箭头定义）。
func svgOpen(w, h int) string {
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="%s">`,
		w, h, w, h, fontFamily) +
		fmt.Sprintf(
			`<defs>
<linearGradient id="bgGrad" x1="0" y1="0" x2="1" y2="1">
<stop offset="0" stop-color="%s"/><stop offset="1" stop-color="%s"/>
</linearGradient>
<marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
<path d="M0,0 L10,5 L0,10 z" fill="%s"/></marker>
</defs>`,
			cBG, cBG2, cMuted) +
		fmt.Sprintf(`<rect width="%d" height="%d" fill="url(#bgGrad)"/>`, w, h)
}

// svgClose 闭合 svg 标签。
func svgClose() string { return `</svg>` }
