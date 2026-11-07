import { useEffect, useRef, useState } from 'react';
import { Button, Empty, Image, Input, Spin, Tag, Typography, message } from 'antd';
import { ArrowLeftOutlined, SendOutlined } from '@ant-design/icons';
import { useNavigate, useParams } from 'react-router-dom';
import { get, post } from '../api/http';
import { figureUrl } from '../types';
import type { ArticleDetail, Figure, FigurePreview } from '../types';
import { KIND_TEXT } from '../components/FigureList';

// 对话气泡：user = 用户指令；assistant = 结果反馈（error 红显），
// 可携带 AI 修改的候选版本预览，待用户确认
interface ChatMsg {
  role: 'user' | 'assistant';
  content: string;
  error?: boolean;
  // AI 修改的候选版本（仅 assistant 消息携带）：确认前文章未变
  preview?: { title: string; spec: string; svg: string };
  // 预览处理状态：pending 待确认 / applying 应用中 / applied 已应用 / discarded 已放弃
  status?: 'pending' | 'applying' | 'applied' | 'discarded';
}

/**
 * 配图 AI 修改页：上半屏大图预览 + 下半屏对话框，多轮迭代——
 * 每轮「指令 → LLM 改 spec → 模板重渲染出预览 → 用户确认 → 应用才落库」，
 * 放弃或不确认则原图保持不变；发送新指令自动作废未确认的预览。
 * 对话历史仅存本页内存（刷新即清），与「指令不落库」的设计一致。
 */
export function FigureModify() {
  const { id, figureId } = useParams();
  const articleId = Number(id);
  const figId = Number(figureId);
  const navigate = useNavigate();

  const [fig, setFig] = useState<Figure | null>(null);
  const [loading, setLoading] = useState(true);
  // 图版本号：每次修改成功 +1，拼进 src 破浏览器缓存拿到新 SVG
  const [rev, setRev] = useState(0);
  const [instruction, setInstruction] = useState('');
  const [sending, setSending] = useState(false);
  // 预览「应用」请求进行中（同一时刻只允许一个应用操作）
  const [applying, setApplying] = useState(false);
  const [msgs, setMsgs] = useState<ChatMsg[]>([]);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!Number.isInteger(articleId) || !Number.isInteger(figId)) return;
    setLoading(true);
    get<ArticleDetail>(`/articles/${articleId}`)
      .then((d) => {
        const target = (d.figures ?? []).find((f) => f.id === figId) ?? null;
        setFig(target);
        if (target && target.kind !== 'cover') {
          setMsgs([
            {
              role: 'assistant',
              content: `这是《${d.title}》的配图「${target.title}」。输入修改指令，AI 会先给出修改预览，你确认后才会更新到文章。内容类：合并前两步 / 把对比重点换成性能；配色类：背景换成海洋蓝 / 换浅色背景（可选主题：深空蓝、海洋蓝、浅色、森林绿）。`,
            },
          ]);
        }
      })
      .catch((e) => message.error((e as Error).message || '文章加载失败'))
      .finally(() => setLoading(false));
  }, [articleId, figId]);

  // 新消息/发送态变化后滚到底部
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [msgs, sending]);

  const onSend = async () => {
    const ins = instruction.trim();
    if (!ins || !fig || sending) return;
    // 新指令基于文章当前版本生成：未确认的旧预览自动作废
    setMsgs((m) => [
      ...m.map((msg) => (msg.status === 'pending' ? { ...msg, status: 'discarded' as const } : msg)),
      { role: 'user' as const, content: ins },
    ]);
    setInstruction('');
    setSending(true);
    try {
      // 与重新生成同理：涉及 LLM 调用，放宽超时
      const pv = await post<FigurePreview>(
        `/articles/${articleId}/figures/${figId}/modify`,
        { instruction: ins },
        { timeout: 300000 },
      );
      // 只产出预览不更新大图；确认（应用）后才落库
      setMsgs((m) => [
        ...m,
        {
          role: 'assistant',
          content: pv.message,
          preview: { title: pv.title, spec: pv.spec, svg: pv.svg },
          status: 'pending',
        },
      ]);
    } catch (e) {
      // 后端保证失败时原图不变，只需在对话流里红显原因
      setMsgs((m) => [...m, { role: 'assistant', content: (e as Error).message || '修改失败', error: true }]);
    } finally {
      setSending(false);
    }
  };

  // 应用预览：把候选版本回传后端重校验落库，成功后大图刷新
  const onApply = async (idx: number) => {
    const msg = msgs[idx];
    if (!msg?.preview || msg.status !== 'pending' || applying) return;
    setApplying(true);
    setMsgs((m) => m.map((x, i) => (i === idx ? { ...x, status: 'applying' } : x)));
    try {
      const meta = await post<Figure>(`/articles/${articleId}/figures/${figId}/apply`, {
        title: msg.preview.title,
        spec: msg.preview.spec,
      });
      setFig((prev) => (prev ? { ...prev, title: meta.title } : prev));
      setRev((v) => v + 1);
      setMsgs((m) => m.map((x, i) => (i === idx ? { ...x, status: 'applied' } : x)));
    } catch (e) {
      // 应用失败回到待确认，预览仍在可重试
      setMsgs((m) => m.map((x, i) => (i === idx ? { ...x, status: 'pending' } : x)));
      message.error((e as Error).message || '应用失败');
    } finally {
      setApplying(false);
    }
  };

  // 放弃预览：不调后端，文章从未变过
  const onDiscard = (idx: number) => {
    setMsgs((m) => m.map((x, i) => (i === idx ? { ...x, status: 'discarded' } : x)));
  };

  const backEdit = () => navigate(`/articles/${articleId}/edit`);

  if (loading) {
    return (
      <div style={{ textAlign: 'center', padding: 48 }}>
        <Spin tip="配图加载中..." />
      </div>
    );
  }
  if (!fig) {
    return (
      <Empty description={`文章 ${articleId} 下不存在配图 #${figId}`}>
        <Button onClick={backEdit}>返回编辑页</Button>
      </Empty>
    );
  }
  if (fig.kind === 'cover') {
    return (
      <Empty description="封面由正文重新生成，不支持按指令修改">
        <Button onClick={backEdit}>返回编辑页</Button>
      </Empty>
    );
  }

  const kind = KIND_TEXT[fig.kind] || { label: fig.kind, color: 'default' };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: 'calc(100vh - 140px)', minHeight: 480 }}>
      {/* 顶栏：返回 + 图信息 */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12, flexWrap: 'wrap' }}>
        <Button icon={<ArrowLeftOutlined />} onClick={backEdit}>
          返回编辑
        </Button>
        <Tag color={kind.color}>{kind.label}</Tag>
        <Typography.Text strong ellipsis style={{ maxWidth: 320 }}>
          {fig.title}
        </Typography.Text>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          #{fig.id} · {'{{figure:'}
          {fig.placeholder}
          {'}}'}
        </Typography.Text>
      </div>

      {/* 大图预览：占主体高度，点击可全屏 */}
      <div
        style={{
          flex: '1 1 auto',
          minHeight: 220,
          overflow: 'auto',
          background: '#0f172a',
          borderRadius: 8,
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'flex-start',
        }}
      >
        <Image
          src={`${figureUrl(fig.id)}?v=${rev}`}
          alt={fig.title}
          style={{ maxHeight: '100%', maxWidth: '100%', width: 'auto' }}
        />
      </div>

      {/* 对话区：消息流 + 输入框 */}
      <div style={{ flex: '0 0 auto', height: 300, display: 'flex', flexDirection: 'column', marginTop: 12 }}>
        <div style={{ flex: 1, overflow: 'auto', padding: '4px 4px 8px' }}>
          {msgs.map((m, i) => (
            <div
              key={i}
              style={{ display: 'flex', justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start', margin: '6px 0' }}
            >
              <div
                style={{
                  maxWidth: m.preview ? '88%' : '78%',
                  padding: '6px 12px',
                  borderRadius: 8,
                  background: m.role === 'user' ? '#1677ff' : m.error ? '#fff1f0' : '#f5f5f5',
                  color: m.role === 'user' ? '#fff' : m.error ? '#cf1322' : 'rgba(0, 0, 0, 0.88)',
                  whiteSpace: 'pre-wrap',
                  fontSize: 13,
                  lineHeight: '20px',
                }}
              >
                {m.content}
                {m.preview && (
                  <div style={{ whiteSpace: 'normal', marginTop: 8 }}>
                    <Image
                      src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(m.preview.svg)}`}
                      alt="修改预览"
                      style={{ width: '100%', borderRadius: 6, display: 'block' }}
                    />
                    {m.status === 'pending' && (
                      <div style={{ display: 'flex', gap: 8, marginTop: 8, alignItems: 'center' }}>
                        <Button type="primary" size="small" loading={applying} onClick={() => void onApply(i)}>
                          应用修改
                        </Button>
                        <Button size="small" disabled={applying} onClick={() => onDiscard(i)}>
                          放弃
                        </Button>
                        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                          确认后才会更新到文章
                        </Typography.Text>
                      </div>
                    )}
                    {m.status === 'applying' && (
                      <div style={{ marginTop: 8 }}>
                        <Tag color="processing">应用中...</Tag>
                      </div>
                    )}
                    {m.status === 'applied' && (
                      <div style={{ marginTop: 8 }}>
                        <Tag color="success">已应用到文章</Tag>
                      </div>
                    )}
                    {m.status === 'discarded' && (
                      <div style={{ marginTop: 8 }}>
                        <Tag>已放弃，文章未变</Tag>
                      </div>
                    )}
                  </div>
                )}
              </div>
            </div>
          ))}
          {sending && (
            <div style={{ display: 'flex', justifyContent: 'flex-start', margin: '6px 0' }}>
              <div style={{ padding: '6px 12px', borderRadius: 8, background: '#f5f5f5', fontSize: 13, color: 'rgba(0,0,0,0.45)' }}>
                AI 修改中，约需十几秒...
              </div>
            </div>
          )}
          <div ref={bottomRef} />
        </div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
          <Input.TextArea
            rows={2}
            maxLength={200}
            showCount
            value={instruction}
            onChange={(e) => setInstruction(e.target.value)}
            placeholder="输入修改指令，Enter 发送（Shift+Enter 换行）"
            onPressEnter={(e) => {
              if (e.shiftKey) return; // Shift+Enter 保留换行
              e.preventDefault();
              void onSend();
            }}
          />
          <Button
            type="primary"
            icon={<SendOutlined />}
            loading={sending}
            disabled={!instruction.trim()}
            onClick={() => void onSend()}
          >
            发送
          </Button>
        </div>
      </div>
    </div>
  );
}
