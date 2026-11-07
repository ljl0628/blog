import { useCallback, useEffect, useState } from 'react';
import {
  Alert,
  Card,
  Col,
  Row,
  Segmented,
  Statistic,
  Table,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { get } from '../api/http';
import type { MetricsDailyRow, MetricsSummary } from '../types';

// 场景中文标签：与后端 scene 值域对齐，未知值原样显示
const SCENE_TEXT: Record<string, string> = {
  analyze: '仓库分析',
  write_article: '文章写作',
  rag_chat: 'RAG 问答',
  embedding: '向量化',
  judge: '质量评审',
  refigure: '配图重生成',
};

// days 切换档位（天）
const DAYS_OPTIONS = [
  { label: '7 天', value: 7 },
  { label: '30 天', value: 30 },
  { label: '90 天', value: 90 },
];

// 按日×场景用量表列定义（后端已按日期倒序返回，直接渲染）
const dailyColumns: ColumnsType<MetricsDailyRow> = [
  { title: '日期', dataIndex: 'date', width: 110 },
  {
    title: '场景',
    dataIndex: 'scene',
    width: 130,
    render: (scene: string) => SCENE_TEXT[scene] ?? scene,
  },
  {
    title: '调用数',
    dataIndex: 'calls',
    width: 90,
    align: 'right',
    render: (n: number) => n.toLocaleString(),
  },
  {
    title: '输入 token',
    dataIndex: 'prompt_tokens',
    width: 110,
    align: 'right',
    render: (n: number) => n.toLocaleString(),
  },
  {
    title: '输出 token',
    dataIndex: 'completion_tokens',
    width: 110,
    align: 'right',
    render: (n: number) => n.toLocaleString(),
  },
  {
    title: '平均耗时',
    dataIndex: 'avg_duration_ms',
    width: 110,
    align: 'right',
    render: (n: number) => `${n.toLocaleString()} ms`,
  },
  {
    title: '成本（元）',
    dataIndex: 'cost',
    width: 110,
    align: 'right',
    // 单行粒度成本常小于 0.01 元，保留 4 位小数避免全显示 0.00
    render: (n: number) => n.toFixed(4),
  },
];

/** 数据看板：LLM 用量总览（汇总卡片）+ 按日×场景明细 */
export function Metrics() {
  const [summary, setSummary] = useState<MetricsSummary | null>(null);
  const [summaryLoading, setSummaryLoading] = useState(false);
  const [summaryErr, setSummaryErr] = useState('');
  const [days, setDays] = useState(30);
  const [rows, setRows] = useState<MetricsDailyRow[]>([]);
  const [dailyLoading, setDailyLoading] = useState(false);
  const [dailyErr, setDailyErr] = useState('');

  // 汇总与 days 无关，仅首屏拉一次
  useEffect(() => {
    setSummaryLoading(true);
    get<MetricsSummary>('/metrics/summary')
      .then(setSummary)
      .catch((e) => setSummaryErr((e as Error).message || '汇总加载失败'))
      .finally(() => setSummaryLoading(false));
  }, []);

  const loadDaily = useCallback(async (d: number) => {
    setDailyLoading(true);
    setDailyErr('');
    try {
      const list = await get<MetricsDailyRow[]>('/metrics/daily', { days: d });
      setRows(list ?? []);
    } catch (e) {
      setDailyErr((e as Error).message || '明细加载失败');
    } finally {
      setDailyLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadDaily(days);
  }, [days, loadDaily]);

  const errorMsg = summaryErr || dailyErr;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Typography.Title level={4} style={{ marginTop: 0 }}>
          数据看板
        </Typography.Title>
        <Segmented
          options={DAYS_OPTIONS}
          value={days}
          onChange={(v) => setDays(Number(v))}
        />
      </div>

      {errorMsg && (
        <Alert
          type="error"
          showIcon
          message={errorMsg}
          style={{ marginBottom: 16 }}
        />
      )}

      <Row gutter={[16, 16]}>
        <Col xs={12} md={8} lg={4}>
          <Card>
            <Statistic
              title="今日成本"
              value={summary?.today_cost ?? 0}
              precision={2}
              suffix=" 元"
              loading={summaryLoading}
            />
          </Card>
        </Col>
        <Col xs={12} md={8} lg={4}>
          <Card>
            <Statistic
              title="本月成本"
              value={summary?.month_cost ?? 0}
              precision={2}
              suffix=" 元"
              loading={summaryLoading}
            />
          </Card>
        </Col>
        <Col xs={12} md={8} lg={4}>
          <Card>
            <Statistic
              title="总 token"
              value={summary?.total_tokens ?? 0}
              loading={summaryLoading}
            />
          </Card>
        </Col>
        <Col xs={12} md={8} lg={4}>
          <Card>
            <Statistic
              title="调用数"
              value={summary?.calls ?? 0}
              loading={summaryLoading}
            />
          </Card>
        </Col>
        <Col xs={12} md={8} lg={4}>
          <Card>
            <Statistic
              title="平均耗时"
              value={summary?.avg_duration_ms ?? 0}
              suffix=" ms"
              loading={summaryLoading}
            />
          </Card>
        </Col>
        <Col xs={12} md={8} lg={4}>
          <Card>
            <Statistic
              title="失败率"
              value={(summary?.error_rate ?? 0) * 100}
              precision={1}
              suffix="%"
              loading={summaryLoading}
            />
          </Card>
        </Col>
      </Row>

      <Card title="按日×场景用量" style={{ marginTop: 16 }}>
        <Table<MetricsDailyRow>
          rowKey={(r) => `${r.date}|${r.scene}`}
          size="small"
          columns={dailyColumns}
          dataSource={rows}
          loading={dailyLoading}
          pagination={{ pageSize: 20, showSizeChanger: false }}
        />
      </Card>
    </div>
  );
}
