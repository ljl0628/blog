import { useCallback, useEffect, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  Form,
  Input,
  Modal,
  Popconfirm,
  Row,
  Space,
  Statistic,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from 'antd';
import { PlayCircleOutlined, PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { Key } from 'react';
import dayjs from 'dayjs';
import { del, get, post, put } from '../api/http';
import { ArticlePicker } from '../components/ArticleSelectModal';
import type {
  RagEvalCase,
  RagEvalCaseSource,
  RagEvalRun,
  RagEvalRunDetailItem,
} from '../types';

// 用例来源 tag：manual = 手动录入；unanswered = 无命中问题回流
const SOURCE_META: Record<RagEvalCaseSource, { text: string; color: string }> = {
  manual: { text: '手动', color: 'blue' },
  unanswered: { text: '回流', color: 'purple' },
};

// 运行状态 tag
const RUN_STATUS_META: Record<string, { text: string; color: string }> = {
  running: { text: '运行中', color: 'processing' },
  done: { text: '已完成', color: 'success' },
  failed: { text: '失败', color: 'error' },
};

// 轮询间隔（ms）
const POLL_INTERVAL = 3000;

// 0~1 小数 → 百分比数值（保留 1 位小数）
function pct(x: number): number {
  return Math.round(x * 1000) / 10;
}

// judge 分保留 2 位小数展示
function round2(x: number): number {
  return Math.round(x * 100) / 100;
}

// detail 后端为原生 JSON 数组（model.JSON 直出）；兼容字符串形态，解析失败按空处理
function parseDetail(
  detail: RagEvalRunDetailItem[] | string | null | undefined,
): RagEvalRunDetailItem[] {
  if (!detail) return [];
  if (Array.isArray(detail)) return detail;
  try {
    const parsed: unknown = JSON.parse(detail);
    return Array.isArray(parsed) ? (parsed as RagEvalRunDetailItem[]) : [];
  } catch {
    return [];
  }
}

// ✓/✗ 结果标记
function Mark({ ok }: { ok: boolean }) {
  return (
    <span style={{ color: ok ? '#389e0d' : '#cf1322', fontWeight: 700 }}>
      {ok ? '✓' : '✗'}
    </span>
  );
}

// 新增/编辑用例表单值：期望文章 ID 由 ArticlePicker 选择回填
interface CaseFormValues {
  question: string;
  expectIds: number[];
}

/** RAG 评测：评测集管理 + 运行与轮询 + 历史结果展开明细 */
export function RagEval() {
  const [cases, setCases] = useState<RagEvalCase[]>([]);
  const [casesLoading, setCasesLoading] = useState(false);
  const [runs, setRuns] = useState<RagEvalRun[]>([]);
  const [currentRun, setCurrentRun] = useState<RagEvalRun | null>(null);
  const [runLoading, setRunLoading] = useState(false);
  const [expandedRunKeys, setExpandedRunKeys] = useState<readonly Key[]>([]);
  // 各轮明细缓存：列表接口不带 detail（大字段），展开时按需拉 /rag-eval/runs/:id
  const [runDetails, setRunDetails] = useState<Record<number, RagEvalRunDetailItem[]>>({});
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<RagEvalCase | null>(null);
  const [saving, setSaving] = useState(false);
  const [togglingId, setTogglingId] = useState<number | null>(null);
  const [form] = Form.useForm<CaseFormValues>();

  const loadCases = useCallback(async () => {
    setCasesLoading(true);
    try {
      const list = await get<RagEvalCase[]>('/rag-eval/cases');
      setCases(list ?? []);
    } catch (e) {
      message.error((e as Error).message || '评测集加载失败');
    } finally {
      setCasesLoading(false);
    }
  }, []);

  // 拉历史列表（后端已倒序），返回最新一条供恢复轮询/当前轮展示
  const loadRuns = useCallback(async (): Promise<RagEvalRun | null> => {
    try {
      const list = await get<RagEvalRun[]>('/rag-eval/runs');
      setRuns(list ?? []);
      return list?.[0] ?? null;
    } catch (e) {
      message.error((e as Error).message || '历史记录加载失败');
      return null;
    }
  }, []);

  // 首屏：拉评测集与历史；最新一轮仍在 running 时恢复轮询
  useEffect(() => {
    void loadCases();
    void loadRuns().then((latest) => setCurrentRun(latest));
  }, [loadCases, loadRuns]);

  // 轮询当前运行：running 期间每 3s 拉一次详情，到 done/failed 停止并刷新历史
  useEffect(() => {
    if (!currentRun || currentRun.status !== 'running') return;
    const runId = currentRun.id;
    const timer = setTimeout(() => {
      get<RagEvalRun>(`/rag-eval/runs/${runId}`)
        .then((run) => {
          setCurrentRun(run);
          if (run.status !== 'running') void loadRuns();
        })
        .catch(() => {
          // 单次查询失败不终止轮询：换新引用触发下一轮
          setCurrentRun((prev) => (prev ? { ...prev } : prev));
        });
    }, POLL_INTERVAL);
    return () => clearTimeout(timer);
  }, [currentRun, loadRuns]);

  // 运行评测：后端已有 running 时返回 409（message 为中文提示）
  const onRun = async () => {
    setRunLoading(true);
    try {
      const run = await post<RagEvalRun>('/rag-eval/run');
      setCurrentRun(run);
      void loadRuns();
      message.success('评测已启动');
    } catch (e) {
      message.warning((e as Error).message || '已有评测进行中');
    } finally {
      setRunLoading(false);
    }
  };

  const onToggleEnabled = async (record: RagEvalCase, enabled: boolean) => {
    setTogglingId(record.id);
    try {
      await put<void>(`/rag-eval/cases/${record.id}`, {
        question: record.question,
        expect_article_ids: record.expect_article_ids,
        enabled,
      });
      message.success(enabled ? '已启用' : '已停用');
      await loadCases();
    } catch (e) {
      message.error((e as Error).message || '操作失败');
    } finally {
      setTogglingId(null);
    }
  };

  const onDelete = async (record: RagEvalCase) => {
    try {
      await del<void>(`/rag-eval/cases/${record.id}`);
      message.success('用例已删除');
      await loadCases();
    } catch (e) {
      message.error((e as Error).message || '删除失败');
    }
  };

  // 打开 Modal 后再回填/重置表单，避免 form 未挂载时报错
  useEffect(() => {
    if (!modalOpen) return;
    if (editing) {
      form.setFieldsValue({
        question: editing.question,
        expectIds: editing.expect_article_ids,
      });
    } else {
      form.resetFields();
    }
  }, [modalOpen, editing, form]);

  const onSubmit = async () => {
    let values: CaseFormValues;
    try {
      values = await form.validateFields();
    } catch {
      return; // 校验失败已由表单提示
    }
    const expectIds = values.expectIds;
    setSaving(true);
    try {
      if (editing) {
        await put<void>(`/rag-eval/cases/${editing.id}`, {
          question: values.question.trim(),
          expect_article_ids: expectIds,
          enabled: editing.enabled,
        });
        message.success('用例已更新');
      } else {
        await post<void>('/rag-eval/cases', {
          question: values.question.trim(),
          expect_article_ids: expectIds,
        });
        message.success('用例已新增');
      }
      setModalOpen(false);
      await loadCases();
    } catch (e) {
      message.error((e as Error).message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const caseColumns: ColumnsType<RagEvalCase> = [
    { title: 'ID', dataIndex: 'id', width: 64 },
    { title: '问题', dataIndex: 'question', ellipsis: true },
    {
      title: '期望文章 ID',
      dataIndex: 'expect_article_ids',
      width: 200,
      render: (ids: number[]) => (
        <Space size={4} wrap>
          {ids.map((id) => (
            <Tag key={id} color="geekblue">
              #{id}
            </Tag>
          ))}
        </Space>
      ),
    },
    {
      title: '来源',
      dataIndex: 'source',
      width: 90,
      render: (s: string) => {
        const meta = SOURCE_META[s as RagEvalCaseSource];
        return meta ? <Tag color={meta.color}>{meta.text}</Tag> : <Tag>{s}</Tag>;
      },
    },
    {
      title: '启用',
      dataIndex: 'enabled',
      width: 80,
      render: (_, record) => (
        <Switch
          size="small"
          checked={record.enabled}
          loading={togglingId === record.id}
          onChange={(checked) => void onToggleEnabled(record, checked)}
        />
      ),
    },
    {
      title: '操作',
      key: 'action',
      width: 130,
      render: (_, record) => (
        <Space size={0}>
          <Button
            type="link"
            size="small"
            onClick={() => {
              setEditing(record);
              setModalOpen(true);
            }}
          >
            编辑
          </Button>
          <Popconfirm
            title="确定删除该评测用例？"
            onConfirm={() => void onDelete(record)}
          >
            <Button type="link" size="small" danger>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const runColumns: ColumnsType<RagEvalRun> = [
    { title: 'ID', dataIndex: 'id', width: 64 },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (s: string) => {
        const meta = RUN_STATUS_META[s];
        return <Tag color={meta?.color ?? 'default'}>{meta?.text ?? s}</Tag>;
      },
    },
    { title: '总数', dataIndex: 'total', width: 70, align: 'right' },
    {
      title: '命中率',
      dataIndex: 'hit_rate',
      width: 90,
      align: 'right',
      render: (v: number) => `${pct(v)}%`,
    },
    {
      title: '引用正确率',
      dataIndex: 'citation_ok_rate',
      width: 100,
      align: 'right',
      render: (v: number) => `${pct(v)}%`,
    },
    {
      title: 'judge 分',
      dataIndex: 'avg_judge_score',
      width: 90,
      align: 'right',
      render: (v: number | null) => (v == null ? '-' : round2(v)),
    },
    {
      title: '时间',
      dataIndex: 'created_at',
      width: 150,
      render: (v: string) => (v ? dayjs(v).format('YYYY-MM-DD HH:mm') : '-'),
    },
  ];

  const detailColumns: ColumnsType<RagEvalRunDetailItem> = [
    { title: '问题', dataIndex: 'question', ellipsis: true },
    {
      title: '命中',
      dataIndex: 'hit',
      width: 64,
      align: 'center',
      render: (ok: boolean, r) => (
        <Tooltip title={`期望 [${r.expect_ids.join(', ')}] / 实际 [${r.actual_ids.join(', ')}]`}>
          <Mark ok={ok} />
        </Tooltip>
      ),
    },
    {
      title: '引用',
      dataIndex: 'citation_ok',
      width: 64,
      align: 'center',
      render: (ok: boolean) => (
        <Tooltip title={ok ? '回答引用了命中的文章' : '回答未正确引用来源'}>
          <Mark ok={ok} />
        </Tooltip>
      ),
    },
    {
      title: 'judge 分',
      dataIndex: 'judge_score',
      width: 90,
      align: 'right',
      render: (v: number | null) => (v == null ? '-' : round2(v)),
    },
    {
      title: '回答摘录',
      dataIndex: 'answer_excerpt',
      ellipsis: true,
      render: (v: string) =>
        v ? (
          <Tooltip title={v}>
            <span>{v}</span>
          </Tooltip>
        ) : (
          '-'
        ),
    },
    {
      title: '错误',
      dataIndex: 'error',
      width: 150,
      ellipsis: true,
      render: (v: string) =>
        v ? (
          <Tooltip title={v}>
            <span style={{ color: '#cf1322' }}>{v}</span>
          </Tooltip>
        ) : (
          '-'
        ),
    },
  ];

  return (
    <div>
      <Typography.Title level={4} style={{ marginTop: 0 }}>
        RAG 评测
      </Typography.Title>

      <Card
        title="评测集"
        extra={
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => {
              setEditing(null);
              setModalOpen(true);
            }}
          >
            新增用例
          </Button>
        }
      >
        <Table<RagEvalCase>
          rowKey="id"
          size="small"
          columns={caseColumns}
          dataSource={cases}
          loading={casesLoading}
          pagination={{ pageSize: 10, showSizeChanger: false }}
        />
      </Card>

      <Card
        title="运行"
        style={{ marginTop: 16 }}
        extra={
          <Tooltip title={currentRun?.status === 'running' ? '已有评测进行中' : ''}>
            <Button
              type="primary"
              icon={<PlayCircleOutlined />}
              loading={runLoading}
              disabled={currentRun?.status === 'running'}
              onClick={() => void onRun()}
            >
              运行评测
            </Button>
          </Tooltip>
        }
      >
        {currentRun?.status === 'running' && (
          <Alert
            type="info"
            showIcon
            message="评测运行中，每 3 秒自动刷新结果"
            style={{ marginBottom: 16 }}
          />
        )}
        {currentRun?.status === 'failed' && (
          <Alert
            type="error"
            showIcon
            message={currentRun.error || '评测失败'}
            style={{ marginBottom: 16 }}
          />
        )}
        <Row gutter={16}>
          <Col xs={12} md={8} lg={4}>
            <Statistic title="总数" value={currentRun?.total ?? '-'} />
          </Col>
          <Col xs={12} md={8} lg={4}>
            <Statistic title="命中数" value={currentRun?.hit_count ?? '-'} />
          </Col>
          <Col xs={12} md={8} lg={4}>
            <Statistic
              title="命中率"
              value={currentRun ? pct(currentRun.hit_rate) : '-'}
              suffix={currentRun ? '%' : ''}
            />
          </Col>
          <Col xs={12} md={8} lg={4}>
            <Statistic
              title="引用正确率"
              value={currentRun ? pct(currentRun.citation_ok_rate) : '-'}
              suffix={currentRun ? '%' : ''}
            />
          </Col>
          <Col xs={12} md={8} lg={4}>
            <Statistic
              title="judge 均分"
              value={
                currentRun?.avg_judge_score == null
                  ? '-'
                  : round2(currentRun.avg_judge_score)
              }
            />
          </Col>
        </Row>
      </Card>

      <Card title="历史记录" style={{ marginTop: 16 }}>
        <Table<RagEvalRun>
          rowKey="id"
          size="small"
          columns={runColumns}
          dataSource={runs}
          pagination={{ pageSize: 10, showSizeChanger: false }}
          expandable={{
            expandedRowKeys: expandedRunKeys,
            onExpandedRowsChange: (keys) => setExpandedRunKeys(keys),
            rowExpandable: () => true,
            expandedRowRender: (run) => (
              <Table<RagEvalRunDetailItem>
                rowKey={(r) => r.case_id}
                size="small"
                columns={detailColumns}
                dataSource={runDetails[run.id] ?? []}
                pagination={false}
              />
            ),
          }}
          onRow={(record) => ({
            style: { cursor: 'pointer' },
            onClick: () => {
              const opening = !expandedRunKeys.includes(record.id);
              setExpandedRunKeys((keys) =>
                keys.includes(record.id)
                  ? keys.filter((k) => k !== record.id)
                  : [...keys, record.id],
              );
              // 首次展开且无缓存时拉取该轮明细（列表接口不携带 detail）
              if (opening && !runDetails[record.id]) {
                get<RagEvalRun>(`/rag-eval/runs/${record.id}`)
                  .then((full) =>
                    setRunDetails((m) => ({
                      ...m,
                      [record.id]: parseDetail(full.detail),
                    })),
                  )
                  .catch(() => {
                    /* 拉取失败保持空明细，不影响其余展示 */
                  });
              }
            },
          })}
        />
      </Card>

      <Modal
        open={modalOpen}
        title={editing ? `编辑用例 #${editing.id}` : '新增评测用例'}
        okText="保存"
        cancelText="取消"
        confirmLoading={saving}
        forceRender
        onOk={() => void onSubmit()}
        onCancel={() => setModalOpen(false)}
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="question"
            label="问题"
            rules={[{ required: true, message: '请输入问题' }]}
          >
            <Input.TextArea
              rows={3}
              maxLength={500}
              showCount
              placeholder="评测时将向 RAG 问答发起的真实问题"
            />
          </Form.Item>
          <Form.Item
            name="expectIds"
            label="期望文章"
            rules={[
              {
                validator: (_rule, value: number[] | undefined) =>
                  value?.length
                    ? Promise.resolve()
                    : Promise.reject(new Error('请至少选择一篇期望文章')),
              },
            ]}
          >
            <ArticlePicker />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
