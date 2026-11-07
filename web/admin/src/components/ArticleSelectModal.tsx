import { useEffect, useState } from 'react';
import { Button, Space, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { get } from '../api/http';
import type { Article, PageData } from '../types';
import { MultiSelectModal } from './MultiSelectModal';

// 已发布文章列表：评测期望命中的文章只能来自向量库（仅已发布文章入索引），
// 故选择范围固定 status=published；文章量为个位到百位，一次拉全（上限 100）
async function fetchPublishedArticles(): Promise<Article[]> {
  const pd = await get<PageData<Article>>('/articles', {
    status: 'published',
    page: 1,
    page_size: 100,
  });
  return pd.items ?? [];
}

// 弹窗展示字段：ID / 标题 / 标签
const columns: ColumnsType<Article> = [
  { title: 'ID', dataIndex: 'id', width: 64 },
  { title: '标题', dataIndex: 'title', ellipsis: true },
  {
    title: '标签',
    dataIndex: 'tags',
    width: 220,
    ellipsis: true,
    render: (tags: string[]) => tags.join(' / '),
  },
];

interface ArticleSelectModalProps {
  open: boolean;
  selected: number[];
  onConfirm: (ids: number[]) => void;
  onCancel: () => void;
}

/** 文章多选弹窗：MultiSelectModal 的文章预设（已发布文章，按 ID/标题/标签搜索） */
export function ArticleSelectModal({
  open,
  selected,
  onConfirm,
  onCancel,
}: ArticleSelectModalProps) {
  return (
    <MultiSelectModal<Article>
      open={open}
      title="选择期望文章"
      fetchOptions={fetchPublishedArticles}
      rowKey={(a) => a.id}
      searchText={(a) => `${a.id} ${a.title} ${a.tags.join(' ')}`}
      columns={columns}
      selected={selected}
      onConfirm={onConfirm}
      onCancel={onCancel}
    />
  );
}

interface ArticlePickerProps {
  value?: number[];
  onChange?: (ids: number[]) => void;
}

// Tag 内标题截断长度（超出显示省略号），避免长标题撑爆表单
const PICKER_TITLE_MAX = 16;

function truncateTitle(title: string): string {
  return title.length > PICKER_TITLE_MAX
    ? `${title.slice(0, PICKER_TITLE_MAX)}…`
    : title;
}

/**
 * 文章选择控件（受控）：Tag 展示已选（尽量带标题）+ 按钮打开选择弹窗。
 * value/onChange 对齐 antd Form.Item 约定，可直接作为表单控件或独立受控使用。
 */
export function ArticlePicker({ value, onChange }: ArticlePickerProps) {
  const [open, setOpen] = useState(false);
  // id → 标题缓存：仅用于 Tag 展示；拉取失败时回退显示 #id，不阻塞选择
  const [titleById, setTitleById] = useState<Record<number, string>>({});

  useEffect(() => {
    fetchPublishedArticles()
      .then((list) => {
        const m: Record<number, string> = {};
        for (const a of list) m[a.id] = a.title;
        setTitleById(m);
      })
      .catch(() => {
        /* 标题展示尽力而为，失败不影响选择功能 */
      });
  }, []);

  return (
    <>
      <Space size={[4, 4]} wrap>
        {(value ?? []).map((id) => (
          <Tag key={id} color="geekblue">
            {titleById[id] ? `#${id} ${truncateTitle(titleById[id])}` : `#${id}`}
          </Tag>
        ))}
        <Button size="small" onClick={() => setOpen(true)}>
          选择文章
        </Button>
      </Space>
      <ArticleSelectModal
        open={open}
        selected={value ?? []}
        onConfirm={(ids) => {
          onChange?.(ids);
          setOpen(false);
        }}
        onCancel={() => setOpen(false)}
      />
    </>
  );
}
