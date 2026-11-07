import { useEffect, useState } from 'react';
import {
  Button,
  Card,
  Col,
  Form,
  Input,
  Popconfirm,
  Row,
  Space,
  Statistic,
  Tag,
  Typography,
  message,
} from 'antd';
import {
  ArrowLeftOutlined,
  CloudUploadOutlined,
  PictureOutlined,
  SaveOutlined,
  StopOutlined,
} from '@ant-design/icons';
import { useNavigate, useParams } from 'react-router-dom';
import { observer } from 'mobx-react-lite';
import dayjs from 'dayjs';
import { articleStore } from '../stores';
import { get } from '../api/http';
import { ARTICLE_STATUS_TEXT } from '../types';
import type { ArticleReview } from '../types';
import { MarkdownPreview } from '../components/MarkdownPreview';
import { FigureList } from '../components/FigureList';
import { ArticleMetaForm } from '../components/ArticleMetaForm';
import type { ArticleMetaValues } from '../components/ArticleMetaForm';

export const ArticleEdit = observer(function ArticleEdit() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [form] = Form.useForm<ArticleMetaValues>();
  const [contentMd, setContentMd] = useState('');
  // 最新质量评审（有 review_score 才拉取并展示评分卡）
  const [review, setReview] = useState<ArticleReview | null>(null);

  const articleId = Number(id);
  const current = articleStore.current;
  const isCurrent = !!current && current.id === articleId;

  useEffect(() => {
    void articleStore.fetchArticle(articleId).catch((e: Error) => {
      message.error(e.message);
    });
  }, [articleId]);

  // 文章加载/切换完成后，把正文同步进编辑器；
  // 不监听 content_md 本身，避免保存或重取详情时覆盖正在编辑的内容
  useEffect(() => {
    if (isCurrent) {
      setContentMd(current?.content_md ?? '');
    }
  }, [isCurrent, current?.id]);

  // 文章带质量分时拉评审明细取最新一条；失败不打断编辑，仅不展示评分卡
  useEffect(() => {
    if (!isCurrent || current?.review_score == null) {
      setReview(null);
      return;
    }
    let cancelled = false;
    get<ArticleReview[]>(`/articles/${articleId}/reviews`)
      .then((list) => {
        if (cancelled) return;
        const latest = [...(list ?? [])].sort(
          (a, b) =>
            new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
        )[0];
        setReview(latest ?? null);
      })
      .catch(() => {
        if (!cancelled) setReview(null);
      });
    return () => {
      cancelled = true;
    };
  }, [isCurrent, current?.review_score, articleId]);

  const onRegenerate = async () => {
    const hide = message.loading('正在重新生成配图（调用 LLM + 渲染），请稍候...', 0);
    try {
      await articleStore.regenerateFigures(articleId);
      message.success('配图已重新生成');
    } catch (e) {
      message.error((e as Error).message || '重新生成配图失败');
    } finally {
      hide();
    }
  };

  const onSave = async () => {
    try {
      const values = await form.validateFields();
      await articleStore.save(articleId, {
        title: values.title.trim(),
        summary: values.summary,
        content_md: contentMd,
        tags: values.tags || [],
      });
      message.success('已保存');
    } catch (e) {
      const msg = (e as Error).message;
      if (msg) message.error(msg || '保存失败');
    }
  };

  const onPublish = async () => {
    try {
      await articleStore.publish(articleId);
      message.success('已发版，前台可见');
    } catch (e) {
      message.error((e as Error).message || '发版失败');
    }
  };

  const onOffline = async () => {
    try {
      await articleStore.offline(articleId);
      message.success('已下线，文章回到待审核状态');
    } catch (e) {
      message.error((e as Error).message || '下线失败');
    }
  };

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 12 }}>
        <Space>
          <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/articles')}>
            返回
          </Button>
          <h3 style={{ margin: 0 }}>编辑文章{isCurrent ? ` #${current.id}` : ''}</h3>
          {isCurrent && (
            <Tag color={current.status === 'published' ? 'success' : 'gold'}>
              {ARTICLE_STATUS_TEXT[current.status] || current.status}
            </Tag>
          )}
        </Space>
        <Space>
          <Popconfirm
            title="重新生成该篇全部配图？"
            description="将重跑 LLM 图表输出与 SVG 渲染，耗时较长。"
            onConfirm={() => void onRegenerate()}
          >
            <Button icon={<PictureOutlined />} loading={articleStore.regenerating}>
              重新生成配图
            </Button>
          </Popconfirm>
          {isCurrent && current.status === 'published' && (
            <Popconfirm title="下线后文章回到待审核状态，确定下线？" onConfirm={() => void onOffline()}>
              <Button icon={<StopOutlined />} danger>
                下线
              </Button>
            </Popconfirm>
          )}
          {isCurrent && current.status !== 'published' && (
            <Popconfirm
              title="确认发版？"
              description="发版后文章在前台立即可见。"
              onConfirm={() => void onPublish()}
            >
              <Button
                type="primary"
                ghost
                icon={<CloudUploadOutlined />}
                loading={articleStore.publishing}
              >
                发版
              </Button>
            </Popconfirm>
          )}
          <Button
            type="primary"
            icon={<SaveOutlined />}
            loading={articleStore.saving}
            onClick={() => void onSave()}
          >
            保存
          </Button>
        </Space>
      </div>

      <ArticleMetaForm
        form={form}
        current={isCurrent ? current : null}
        loading={!isCurrent && articleStore.detailLoading}
      />

      {/* 质量评审卡：总分大字 + 结论 + 四维明细 + 评语；仅有评审记录的文章显示 */}
      {review && (
        <Card
          size="small"
          style={{ marginTop: 16 }}
          title="质量评审"
          extra={
            <Typography.Text type="secondary">
              {review.judge_model} ·{' '}
              {dayjs(review.created_at).format('YYYY-MM-DD HH:mm')}
            </Typography.Text>
          }
        >
          <Row gutter={24} align="middle">
            <Col xs={24} sm={6} md={4} style={{ textAlign: 'center' }}>
              <div
                style={{
                  fontSize: 40,
                  fontWeight: 700,
                  lineHeight: 1.2,
                  color:
                    review.score_total >= 4
                      ? '#389e0d'
                      : review.score_total >= 3
                        ? '#d46b08'
                        : '#cf1322',
                }}
              >
                {review.score_total}
              </div>
              <Tag
                color={review.verdict === 'pass' ? 'success' : 'warning'}
                style={{ marginTop: 8 }}
              >
                {review.verdict === 'pass' ? '通过' : '需复核'}
              </Tag>
            </Col>
            <Col xs={24} sm={18} md={20}>
              <Row gutter={16}>
                <Col xs={12} md={6}>
                  <Statistic title="准确" value={review.score_accuracy} suffix="/ 5" />
                </Col>
                <Col xs={12} md={6}>
                  <Statistic title="结构" value={review.score_structure} suffix="/ 5" />
                </Col>
                <Col xs={12} md={6}>
                  <Statistic title="可读性" value={review.score_readability} suffix="/ 5" />
                </Col>
                <Col xs={12} md={6}>
                  <Statistic title="配图" value={review.score_figure} suffix="/ 5" />
                </Col>
              </Row>
              {review.comment && (
                <Typography.Paragraph
                  type="secondary"
                  style={{ marginTop: 12, marginBottom: 0 }}
                >
                  {review.comment}
                </Typography.Paragraph>
              )}
            </Col>
          </Row>
        </Card>
      )}

      <Row gutter={16}>
        <Col xs={24} lg={12}>
          <Card title="Markdown 正文" size="small">
            <Input.TextArea
              className="editor-textarea"
              value={contentMd}
              onChange={(e) => setContentMd(e.target.value)}
              placeholder="支持 Markdown 与 {{figure:xxx}} 配图占位"
              style={{ height: '62vh', resize: 'none' }}
            />
          </Card>
        </Col>
        <Col xs={24} lg={12}>
          <Card title="实时预览" size="small">
            <MarkdownPreview content={contentMd} figures={current?.figures || []} />
          </Card>
        </Col>
      </Row>

      <Card title="配图预览" size="small" style={{ marginTop: 16 }}>
        <FigureList figures={isCurrent ? current.figures : []} loading={!isCurrent && articleStore.detailLoading} />
      </Card>
    </div>
  );
});
