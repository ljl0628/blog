import { useEffect, useMemo, useState } from 'react';
import { Input, Modal, Table, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { Key } from 'react';

// 通用多选弹窗 props：调用方提供数据源与显示字段，组件负责加载/搜索/勾选
export interface MultiSelectModalProps<T> {
  open: boolean;
  title: string;
  /** 候选列表数据源：弹窗打开时调用（应走 api/http 的列表接口） */
  fetchOptions: () => Promise<T[]>;
  /** 行唯一 ID：勾选值即该 ID 的数组 */
  rowKey: (record: T) => number;
  /** 搜索文本提取：提供时显示搜索框并按其返回值本地过滤 */
  searchText?: (record: T) => string;
  /** 显示字段：表格列定义（勾选列由组件附加，无需包含） */
  columns: ColumnsType<T>;
  /** 已选 ID：作为弹窗打开时刻的初始勾选 */
  selected: number[];
  onConfirm: (ids: number[]) => void;
  onCancel: () => void;
}

/**
 * 通用多选弹窗：打开时拉取候选列表 → 本地搜索 + 勾选 → 确定回传 ID 数组。
 * 列表接口与显示字段由调用方注入，组件不感知具体业务。
 */
export function MultiSelectModal<T>({
  open,
  title,
  fetchOptions,
  rowKey,
  searchText,
  columns,
  selected,
  onConfirm,
  onCancel,
}: MultiSelectModalProps<T>) {
  const [options, setOptions] = useState<T[]>([]);
  const [loading, setLoading] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [selectedKeys, setSelectedKeys] = useState<Key[]>([]);

  // 打开时拉取候选并按 selected 快照初始勾选；关闭后清搜索词。
  // 只依赖 open：fetchOptions/selected 以打开时刻为准，期间父组件重渲染不重复拉取。
  useEffect(() => {
    if (!open) return;
    setKeyword('');
    setSelectedKeys(selected);
    setLoading(true);
    fetchOptions()
      .then(setOptions)
      .catch((e) => message.error((e as Error).message || '列表加载失败'))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase();
    if (!kw || !searchText) return options;
    return options.filter((o) => searchText(o).toLowerCase().includes(kw));
  }, [options, keyword, searchText]);

  return (
    <Modal
      open={open}
      title={title}
      okText={`确定${selectedKeys.length ? `(${selectedKeys.length})` : ''}`}
      cancelText="取消"
      width={720}
      onOk={() => onConfirm(selectedKeys.map(Number))}
      onCancel={onCancel}
    >
      {searchText && (
        <Input.Search
          allowClear
          placeholder="输入关键词过滤"
          value={keyword}
          onChange={(e) => setKeyword(e.target.value)}
          style={{ marginBottom: 12 }}
        />
      )}
      <Table<T>
        rowKey={rowKey}
        size="small"
        columns={columns}
        dataSource={filtered}
        loading={loading}
        pagination={{ pageSize: 8, showSizeChanger: false }}
        rowSelection={{
          selectedRowKeys: selectedKeys,
          onChange: setSelectedKeys,
          // 搜索/翻页后 dataSource 变化时保留已勾选项
          preserveSelectedRowKeys: true,
        }}
      />
    </Modal>
  );
}
