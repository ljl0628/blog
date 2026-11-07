// RAG 评测（与后端 rag-eval 接口响应结构对齐，JSON tag 同名）

// 评测用例来源：manual = 手动录入；unanswered = 无命中问题回流
export type RagEvalCaseSource = 'manual' | 'unanswered';

// 评测用例（GET /rag-eval/cases）
export interface RagEvalCase {
  id: number;
  question: string;
  expect_article_ids: number[];
  source: RagEvalCaseSource;
  enabled: boolean;
  created_at: string;
}

// 新增用例（POST /rag-eval/cases）
export interface RagEvalCaseCreatePayload {
  question: string;
  expect_article_ids: number[];
}

// 更新用例（PUT /rag-eval/cases/:id）
export interface RagEvalCaseUpdatePayload extends RagEvalCaseCreatePayload {
  enabled: boolean;
}

// 无命中问题导入为评测用例（POST /rag-eval/cases/import-unanswered）
export interface RagEvalImportUnansweredPayload {
  unanswered_id: number;
  expect_article_ids: number[];
}

// 评测运行状态
export type RagEvalRunStatus = 'running' | 'done' | 'failed';

// 评测运行记录（GET /rag-eval/runs[/id]）。detail 由后端 model.JSON（json.RawMessage）
// 直接序列化为原生 JSON 数组；列表接口不携带该字段。
export interface RagEvalRun {
  id: number;
  status: RagEvalRunStatus;
  total: number;
  hit_count: number;
  hit_rate: number; // 0~1 小数
  citation_ok_rate: number; // 0~1 小数
  avg_judge_score: number | null; // 无 judge 结果时为 null
  detail?: RagEvalRunDetailItem[] | string | null;
  error: string;
  created_at: string;
}

// detail 解析后的单用例结果
export interface RagEvalRunDetailItem {
  case_id: number;
  question: string;
  hit: boolean;
  expect_ids: number[];
  actual_ids: number[];
  citation_ok: boolean;
  judge_score: number | null;
  answer_excerpt: string;
  error: string;
}
