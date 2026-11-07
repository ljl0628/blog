import { Button, Card, Col, Empty, Image, Row, Spin, Tag, Typography } from 'antd';
import { EditOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import type { Figure } from '../types';
import { figureUrl } from '../types';

// 配图类型文案：FigureList 卡片与 FigureModify 页共用
export const KIND_TEXT: Record<string, { label: string; color: string }> = {
  cover: { label: '封面', color: 'purple' },
  architecture: { label: '架构图', color: 'blue' },
  flow: { label: '流程图', color: 'cyan' },
  compare: { label: '对比', color: 'orange' },
  timeline: { label: '时间线', color: 'green' },
};

export function FigureList({
  figures,
  loading,
}: {
  figures?: Figure[];
  loading?: boolean;
}) {
  const navigate = useNavigate();

  if (loading) {
    return (
      <div style={{ textAlign: 'center', padding: 24 }}>
        <Spin tip="配图加载中..." />
      </div>
    );
  }
  if (!figures || figures.length === 0) {
    return <Empty description="暂无配图" image={Empty.PRESENTED_IMAGE_SIMPLE} />;
  }

  return (
    <Image.PreviewGroup>
      <Row gutter={[12, 12]}>
        {figures.map((fig) => {
          const kind = KIND_TEXT[fig.kind] || { label: fig.kind, color: 'default' };
          // 封面由正文重新生成，不提供指令修改
          const modifiable = fig.kind !== 'cover';
          return (
            <Col key={fig.id} xs={24} sm={12} md={8} lg={6}>
              <Card
                size="small"
                cover={
                  // antd Image：点击放大预览（PreviewGroup 支持组内左右切换）
                  <Image
                    src={figureUrl(fig.id)}
                    alt={fig.title}
                    style={{ width: '100%', background: '#0f172a', minHeight: 100 }}
                  />
                }
                actions={
                  modifiable
                    ? [
                        <Button
                          key="modify"
                          type="link"
                          size="small"
                          icon={<EditOutlined />}
                          onClick={() => navigate(`/articles/${fig.article_id}/figures/${fig.id}`)}
                        >
                          AI 修改
                        </Button>,
                      ]
                    : undefined
                }
              >
                <Card.Meta
                  title={
                    <>
                      <Tag color={kind.color}>{kind.label}</Tag>
                      <Typography.Text style={{ fontSize: 13 }}>{fig.title}</Typography.Text>
                    </>
                  }
                  description={`#${fig.id}${fig.placeholder ? ` · {{figure:${fig.placeholder}}}` : ''}`}
                />
              </Card>
            </Col>
          );
        })}
      </Row>
    </Image.PreviewGroup>
  );
}
