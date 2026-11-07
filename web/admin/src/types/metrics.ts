// LLM 用量指标（与后端 metrics 接口响应结构对齐，JSON tag 同名）

// 用量总览（GET /metrics/summary）
export interface MetricsSummary {
  today_cost: number;
  month_cost: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  calls: number;
  avg_duration_ms: number;
  error_rate: number; // 0~1 小数
}

// 按日×场景用量行（GET /metrics/daily，后端已按日期倒序）
export interface MetricsDailyRow {
  date: string;
  scene: string;
  calls: number;
  prompt_tokens: number;
  completion_tokens: number;
  avg_duration_ms: number;
  cost: number;
}
