// 文章与配图（与后端 model.Article / model.SvgAsset 对齐，JSON tag 同名）

// 文章状态：draft（待审核）→ publish → published → offline 回到 draft
export type ArticleStatus = 'draft' | 'published' | 'offline';

export interface Article {
  id: number;
  repo_analysis_id: number;
  title: string;
  slug: string;
  summary: string;
  content_md?: string;
  word_count: number;
  like_count?: number;
  tags: string[];
  status: ArticleStatus;
  sort_order: number;
  cover_asset_id?: number | null;
  published_at?: string | null;
  created_at: string;
  updated_at: string;
  /** 质量评审总分（0~5，满分 5）；未评审时为 null */
  review_score?: number | null;
}

// SVG 配图（svg_asset），正文通过 {{figure:xxx}} 占位
export interface Figure {
  id: number;
  article_id: number;
  placeholder: string;
  kind: 'cover' | 'architecture' | 'flow' | 'compare' | 'timeline' | string;
  title: string;
  created_at: string;
  /** AI 修改接口返回的实际变化摘要（其余场景无此字段） */
  message?: string;
}

// 配图 AI 修改的预览结果（modify 接口返回，未落库）：
// 用户点「应用」走 apply 接口后才会更新文章
export interface FigurePreview {
  title: string;
  /** 新的图数据 JSON 文本（含可选 theme 字段），应用时原样回传 */
  spec: string;
  /** 预览渲染的 SVG（确定性渲染，应用后与之一致） */
  svg: string;
  /** 如实变更摘要（改了标题/配色/数据哪些维度，或告知未变化） */
  message: string;
}

export interface ArticleDetail extends Article {
  figures?: Figure[];
}

export interface ArticleUpdatePayload {
  title: string;
  summary: string;
  content_md: string;
  tags: string[];
}

// 文章质量评审结论：pass = 通过；needs_review = 需人工复核
export type ArticleReviewVerdict = 'pass' | 'needs_review';

// 质量评审记录（GET /articles/:id/reviews，编辑页取最新一条展示四维明细）
export interface ArticleReview {
  id: number;
  article_id: number;
  score_total: number;
  score_accuracy: number;
  score_structure: number;
  score_readability: number;
  score_figure: number;
  verdict: ArticleReviewVerdict;
  judge_model: string;
  comment: string;
  created_at: string;
}

export const ARTICLE_STATUS_TEXT: Record<string, string> = {
  draft: '待审核',
  published: '已发布',
  offline: '已下线',
};

// 前台配图访问地址（经 vite proxy 转发到 Go 后端）
export function figureUrl(id: number): string {
  return `/api/v1/portal/figures/${id}`;
}
