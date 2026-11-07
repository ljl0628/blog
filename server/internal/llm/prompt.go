package llm

import (
	"fmt"
	"strings"
)

const analysisSystem = `你是一位资深软件架构师和技术写作专家，擅长分析开源项目的代码库，` +
	`能精准识别项目的技术栈、架构设计与代码亮点。你必须严格按用户要求的 JSON 结构输出，` +
	`直接输出 JSON，不要包含任何解释、markdown 代码块或多余文本。所有内容使用中文（专有名词、技术术语保留英文）。`

// BuildAnalysisPrompt 构建仓库分析阶段的 system / user 提示词。
// digest 为已拼装好的仓库材料（语言统计、目录树、README 摘要、采样代码文件）。
func BuildAnalysisPrompt(digest string, articleCount int) (system, user string) {
	system = analysisSystem
	user = fmt.Sprintf(`请深入分析下面这个 Git 仓库的代码材料，输出 JSON。

== 仓库材料开始 ==
%s
== 仓库材料结束 ==

输出 JSON 结构如下：
{
  "repo_name": "仓库名称",
  "tech_stack": ["技术栈清单，按重要程度排序，10 项以内，如 Go、Gin、GORM、MySQL、Redis"],
  "summary": "仓库整体分析，300 字以内：项目定位、架构风格、代码组织",
  "highlights": ["代码/架构亮点，3~6 条，每条一句话，指向具体的设计或写法"],
  "article_plan": [
    {
      "title": "文章标题，有吸引力但不标题党",
      "slug": "english-kebab-case-slug",
      "summary": "文章摘要，80 字以内",
      "tags": ["2~4 个标签"],
      "outline": ["章节标题，4~6 个"],
      "target_words": 2000
    }
  ]
}

article_plan 必须包含恰好 %d 篇文章，选题固定如下（顺序不可变，标题可自行润色）：
1. 第 1 篇「技术栈全景解读」：盘点该仓库使用的全部技术栈及选型理由，target_words=2000，配图用 compare（对比同类方案）或 timeline；
2. 第 2 篇「架构设计解析」：剖析项目整体架构与模块划分，target_words=2500，配图必须包含 architecture（整体架构图）；
3. 第 3 篇「优雅代码写法赏析」：从采样代码中挑选最能体现工程功底的片段进行讲解，target_words=2500，配图用 flow（关键流程图）；
4. 第 4 篇「工程实践与最佳实践总结」：总结项目的工程化实践（目录组织、错误处理、并发、测试、CI 等）与可借鉴经验，target_words=1800，配图用 timeline（演进/实践清单）或 flow。

直接输出 JSON。`, digest, articleCount)
	return system, user
}

const writerSystem = `你是一位顶级技术博客作者，文风清晰、有洞察力，善于把复杂的工程实现讲得通俗易懂又不失深度。` +
	`你必须严格按用户要求的 JSON 结构输出，直接输出 JSON，不要包含任何解释、markdown 代码块或多余文本。` +
	`正文使用中文撰写，专有名词、代码、命令保留英文。`

// BuildArticlePrompt 构建单篇文章写作的 system / user 提示词。
// analysisJSON 为分析阶段输出原文，planJSON 为本篇选题。
func BuildArticlePrompt(repoName string, analysisJSON string, planJSON string) (system, user string) {
	system = writerSystem
	user = fmt.Sprintf(`基于对仓库「%s」的分析结果，撰写其中一篇文章，输出 JSON。

== 仓库分析结果 ==
%s
== 本篇选题 ==
%s
== 写作要求 ==
1. markdown 字段：完整 Markdown 正文。第一个一级标题即文章标题；正文用二级/三级标题分节，沿用选题大纲结构。
2. 篇幅达到 target_words 的 ±20%%（按中文字符计），内容要具体、有细节，禁止空话套话和逐条罗列式的灌水。
3. 必须引用仓库中真实存在的代码片段（来自分析材料），用对应语言的 markdown 代码块展示，并讲解其设计意图与优雅之处；禁止编造不存在的代码。
4. 图文结合：在正文合适位置插入 1~3 个配图占位符，格式为 {{figure:fig-1}}、{{figure:fig-2}}（从 fig-1 顺序编号），每个占位符必须独立成段（前后空行）。
5. figures 数组与正文占位符一一对应，每个元素结构：
   { "id": "fig-1", "kind": "architecture|flow|compare|timeline", "title": "图标题", "data": { ... } }
   data 按 kind 区分：
   - architecture: { "layers": [ { "name": "层名", "nodes": ["节点名", "..."] } ], "edges": [ { "from": "上层某节点名", "to": "下层某节点名", "label": "可选说明" } ] }
     layers 按从上到下顺序 2~4 层，每层 2~4 个节点；edges 的 from/to 必须是 nodes 中已存在的节点名。
   - flow: { "steps": [ { "name": "步骤名", "desc": "一句话说明" } ] }，3~8 个步骤。
   - compare: { "left": { "title": "方案A", "points": ["要点", "..."] }, "right": { "title": "方案B", "points": ["要点", "..."] } }，每侧 3~5 个要点，每个要点 30 字以内。
   - timeline: { "items": [ { "time": "时间/版本", "title": "事件", "desc": "一句话说明" } ] }，3~6 项。
6. slug 为英文 kebab-case；summary 80 字以内；tags 2~4 个。

输出 JSON 结构：
{ "title": "...", "slug": "...", "summary": "...", "tags": ["..."], "markdown": "完整正文", "figures": [ ... ] }

直接输出 JSON。`, repoName, analysisJSON, planJSON)
	return system, user
}

// BuildFigureRegenPrompt 构建配图重新生成的 system / user 提示词。
// figsMeta 为已有配图的 id/kind/title 清单，要求模型仅更新 data。
func BuildFigureRegenPrompt(title string, markdown string, figsMeta string) (system, user string) {
	system = writerSystem
	user = fmt.Sprintf(`下面是一篇技术文章和它已有配图的元信息。请重新设计这些配图的数据（保持 id 与 kind 不变，可优化 title），让配图更贴合正文内容。

== 文章标题 ==
%s

== 文章正文 ==
%s

== 已有配图元信息 ==
%s

输出 JSON 结构（figures 数组元素数、id、kind 必须与已有配图元信息完全一致）：
{ "figures": [ { "id": "fig-1", "kind": "与元信息一致", "title": "图标题", "data": { ... } } ] }

data 按 kind 的结构与要求（data 顶层直接是该类型的字段，如 flow 的顶层就是 steps，不要再包一层类型名）：
- architecture: { "layers": [ { "name": "层名", "nodes": ["节点名"] } ], "edges": [ { "from": "某节点名", "to": "某节点名", "label": "可选" } ] }，2~4 层，每层 2~4 个节点，edges 引用已存在的节点名。
- flow: { "steps": [ { "name": "步骤名", "desc": "一句话说明" } ] }，3~8 步。
- compare: { "left": { "title": "A", "points": ["..."] }, "right": { "title": "B", "points": ["..."] } }，每侧 3~5 点、每点 30 字以内。
- timeline: { "items": [ { "time": "...", "title": "...", "desc": "一句话" } ] }，3~6 项。

data 可含可选字段 "theme"（midnight 深空蓝默认 / ocean 海洋蓝 / light 浅色 / forest 森林绿），按文章气质选配整图配色。

直接输出 JSON。`, title, markdown, figsMeta)
	return system, user
}

// BuildFigureFillPrompt 构建补齐缺失配图的 system / user 提示词：
// missingIDs 为正文中悬空的占位符 id 清单（逗号分隔），kind 由模型按上下文自选。
func BuildFigureFillPrompt(title string, markdown string, missingIDs string) (system, user string) {
	system = writerSystem
	user = fmt.Sprintf(`下面是一篇技术文章，正文中部分配图占位符（形如 {{figure:fig-2}}）还没有对应的配图数据。请阅读每个占位符所在位置的上下文，为其设计一张贴合内容的配图。

== 文章标题 ==
%s

== 文章正文 ==
%s

== 缺失的占位符 id 清单 ==
%s

输出 JSON 结构（figures 数组每个 id 必须取自缺失清单，不重复、不遗漏；kind 从 architecture/flow/compare/timeline 中选择最贴合内容的类型）：
{ "figures": [ { "id": "fig-2", "kind": "flow", "title": "图标题", "data": { ... } } ] }

data 按 kind 的结构与要求（data 顶层直接是该类型的字段，如 flow 的顶层就是 steps，不要再包一层类型名）：
- architecture: { "layers": [ { "name": "层名", "nodes": ["节点名"] } ], "edges": [ { "from": "某节点名", "to": "某节点名", "label": "可选" } ] }，2~4 层，每层 2~4 个节点，edges 引用已存在的节点名。
- flow: { "steps": [ { "name": "步骤名", "desc": "一句话说明" } ] }，3~8 步。
- compare: { "left": { "title": "A", "points": ["..."] }, "right": { "title": "B", "points": ["..."] } }，每侧 3~5 点、每点 30 字以内。
- timeline: { "items": [ { "time": "...", "title": "...", "desc": "一句话" } ] }，3~6 项。

data 可含可选字段 "theme"（midnight 深空蓝默认 / ocean 海洋蓝 / light 浅色 / forest 森林绿），按文章气质选配整图配色。

直接输出 JSON。`, title, markdown, missingIDs)
	return system, user
}

// BuildFigureModifyPrompt 构建单张配图按指令 AI 修改的 system / user 提示词。
// spec 为该图当前数据（JSON 文本），instruction 为用户修改指令；要求模型
// 在现有数据基础上按指令做最小改动，而非脱离原图自由发挥。
func BuildFigureModifyPrompt(title, markdown, figKind, figTitle, spec, instruction string) (system, user string) {
	system = writerSystem
	user = fmt.Sprintf(`下面是一篇技术文章中一张配图的当前数据和一个修改指令。请在现有数据的基础上按指令修改这张图的数据（保持 kind 不变），只做指令要求的改动，不要脱离原图自由发挥；title 原样保留，仅当指令明确要求改标题时才修改。

== 文章标题 ==
%s

== 文章正文 ==
%s

== 配图类型 ==
%s（当前标题：%s）

== 当前图数据（JSON）==
%s

== 修改指令 ==
%s

输出 JSON 结构：
{ "title": "图标题", "theme": "可选，配色主题", "data": { ... } }

data 按 kind 的结构与要求（data 顶层直接是该类型的字段，如 flow 的顶层就是 steps，不要再包一层类型名）：
- architecture: { "layers": [ { "name": "层名", "nodes": ["节点名"] } ], "edges": [ { "from": "某节点名", "to": "某节点名", "label": "可选" } ] }，2~4 层，每层 2~4 个节点，edges 引用已存在的节点名。
- flow: { "steps": [ { "name": "步骤名", "desc": "一句话说明" } ] }，3~8 步。
- compare: { "left": { "title": "A", "points": ["..."] }, "right": { "title": "B", "points": ["..."] } }，每侧 3~5 点、每点 30 字以内。
- timeline: { "items": [ { "time": "...", "title": "...", "desc": "一句话" } ] }，3~6 项。

两条硬性规则：
1. 无论指令如何表述，输出的 data 必须保有该 kind 的完整结构与主体条目（如 flow 不得少于 3 步），绝不输出空数组或清空内容。
2. 指令若是在指出数据缺失或有误（如"数据去哪了""流程不见了"），应依据正文内容把 data 修复补全，而不是只改标题。

theme 可选顶层字段，控制整图配色背景，取值 midnight（深空蓝，默认）/ ocean（海洋蓝）/ light（浅色）/ forest（森林绿）。指令同时涉及内容与配色时才输出 theme（纯配色指令后端直接处理，到不了你这里）；仅内容指令时不要输出 theme 字段，data 数据内容原样保留。

直接输出 JSON。`, title, markdown, figKind, figTitle, spec, instruction)
	return system, user
}

// BuildFigureRebuildPrompt 构建配图数据重建的 system / user 提示词。
// 当前图数据已损坏（主体条目为空）时使用：不传坏基线，要求模型依据
// 正文与指令为该图全新设计一份数据，走出「在空数据上最小改动」的死局。
func BuildFigureRebuildPrompt(title, markdown, figKind, figTitle, instruction string) (system, user string) {
	system = writerSystem
	user = fmt.Sprintf(`下面是一篇技术文章。其中一张配图（类型 %s，标题 %q）的数据已损坏丢失，需要你依据文章内容为它重新设计一份数据；用户指令表达了对这张图的期望，优先满足。

== 文章标题 ==
%s

== 文章正文 ==
%s

== 配图类型与标题 ==
%s（%s）

== 用户指令 ==
%s

输出 JSON 结构：
{ "title": "图标题", "data": { ... } }

data 按 kind 的结构与要求（data 顶层直接是该类型的字段，如 flow 的顶层就是 steps，不要再包一层类型名）：
- architecture: { "layers": [ { "name": "层名", "nodes": ["节点名"] } ], "edges": [ { "from": "某节点名", "to": "某节点名", "label": "可选" } ] }，2~4 层，每层 2~4 个节点，edges 引用已存在的节点名。
- flow: { "steps": [ { "name": "步骤名", "desc": "一句话说明" } ] }，3~8 步。
- compare: { "left": { "title": "A", "points": ["..."] }, "right": { "title": "B", "points": ["..."] } }，每侧 3~5 点、每点 30 字以内。
- timeline: { "items": [ { "time": "...", "title": "...", "desc": "一句话" } ] }，3~6 项。

title 原样保留（%q），仅当指令明确要求改标题时才修改。可选顶层字段 theme 控制整图配色背景，取值 midnight（深空蓝，默认）/ ocean（海洋蓝）/ light（浅色）/ forest（森林绿）。

直接输出 JSON。`, figKind, figTitle, title, markdown, figKind, figTitle, instruction, figTitle)
	return system, user
}

const judgeSystem = `你是一位严格的技术博客审校编辑，以挑剔的眼光对 AI 生成的技术文章做质量把关，` +
	`打分客观、不恭维。你必须严格按用户要求的 JSON 结构输出，` +
	`直接输出 JSON，不要包含任何解释、markdown 代码块或多余文本。评语使用中文。`

// BuildJudgePrompt 构建文章质量评审（judge）阶段的 system / user 提示词。
// markdown 为文章正文，figuresJSON 为配图 spec 数组的 JSON 文本（评配图维度用）。
func BuildJudgePrompt(repoName, title, summary, markdown string, figuresJSON string) (system, user string) {
	system = judgeSystem
	user = fmt.Sprintf(`请评审下面这篇基于仓库「%s」生成的技术博客文章，按四个维度各打 1~5 分（可含一位小数），输出 JSON。

== 文章标题 ==
%s

== 文章摘要 ==
%s

== 文章正文 ==
%s

== 配图 spec（与正文 {{figure:...}} 占位符对应）==
%s

评分维度：
1. score_accuracy 准确性：技术表述与代码引用是否可信、有无编造或明显错误；
2. score_structure 结构性：层次划分与论证逻辑是否清晰完整；
3. score_readability 可读性：语言是否流畅、深度与目标受众适配是否得当；
4. score_figure 配图：figures spec 与正文内容的匹配度与信息准确性。

输出 JSON 结构如下：
{
  "score_accuracy": 4.5,
  "score_structure": 4.0,
  "score_readability": 4.0,
  "score_figure": 3.5,
  "score_total": 4.0,
  "verdict": "pass",
  "comment": "一句中文评语，指出最大的优点或问题，200 字以内"
}

规则：score_total = 四维分数的平均值，保留一位小数；verdict：score_total ≥ 3.5 为 "pass"，否则为 "needs_review"。

直接输出 JSON。`, repoName, title, summary, markdown, figuresJSON)
	return system, user
}

// Digest 由 pipeline 组装仓库材料的格式约定放在这里，便于测试对齐。
func DigestSection(title, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n%s\n", title, body)
	return b.String()
}
