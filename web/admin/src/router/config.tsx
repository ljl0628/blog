import {
  BarChartOutlined,
  DatabaseOutlined,
  DashboardOutlined,
  ExperimentOutlined,
  FileTextOutlined,
  QuestionCircleOutlined,
  RocketOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { Login } from '../pages/Login';
import { Dashboard } from '../pages/Dashboard';
import { Metrics } from '../pages/Metrics';
import { Tasks } from '../pages/Tasks';
import { TaskLogs } from '../pages/TaskLogs';
import { Articles } from '../pages/Articles';
import { ArticleEdit } from '../pages/ArticleEdit';
import { FigureModify } from '../pages/FigureModify';
import { RagIndex } from '../pages/RagIndex';
import { RagEval } from '../pages/RagEval';
import { AskUnansweredPage } from '../pages/AskUnanswered';
import { AccountSettings } from '../pages/AccountSettings';
import type { AppRoute } from '../types';

/**
 * 路由配置表（纯配置，派生逻辑见 ./index）：加页面 = 这里加一条。
 * - PUBLIC_ROUTES：无登录布局的页面（登录页）；
 * - APP_ROUTES：AdminLayout 下的业务页面，带 title 的自动进侧边菜单，
 *   详情/编辑类页面（title 省略）只注册路由不进菜单。
 */

export const PUBLIC_ROUTES: AppRoute[] = [{ path: '/login', element: <Login /> }];

export const APP_ROUTES: AppRoute[] = [
  { path: '/', title: '工作台', icon: <DashboardOutlined />, element: <Dashboard /> },
  // 数据看板紧随工作台：同为全局观察类入口，使用频率高于任务/文章等业务页
  { path: '/metrics', title: '数据', icon: <BarChartOutlined />, element: <Metrics /> },
  { path: '/tasks', title: '任务', icon: <RocketOutlined />, element: <Tasks /> },
  { path: '/tasks/:id/logs', element: <TaskLogs /> },
  { path: '/articles', title: '文章管理', icon: <FileTextOutlined />, element: <Articles /> },
  { path: '/articles/:id/edit', element: <ArticleEdit /> },
  // 配图 AI 修改页：大图预览 + 对话式指令（不进菜单，从编辑页配图卡进入）
  { path: '/articles/:id/figures/:figureId', element: <FigureModify /> },
  { path: '/rag-index', title: 'RAG 索引', icon: <DatabaseOutlined />, element: <RagIndex /> },
  // RAG 评测紧随 RAG 索引：索引运营与效果验收的配套入口
  { path: '/rag-eval', title: 'RAG 评测', icon: <ExperimentOutlined />, element: <RagEval /> },
  {
    path: '/ask-unanswered',
    title: '无命中问题',
    icon: <QuestionCircleOutlined />,
    element: <AskUnansweredPage />,
  },
  {
    path: '/account',
    title: '账户设置',
    icon: <UserOutlined />,
    element: <AccountSettings />,
  },
];
