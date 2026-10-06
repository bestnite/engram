import type { LocaleCatalog } from '../types';

/**
 * 简体中文语言包骨架（DESIGN.md §8.3、§10.4）
 * key 一律英文小写点分。与 en.ts 的 key 集合必须完全一致。
 */
export const zhCN: LocaleCatalog = {
  'app.name': 'Engram',
  'nav.today': '今日',
  'nav.decks': '卡组',
  'nav.stats': '统计',
  'nav.settings': '设置',
  'nav.login': '登录',
  'nav.logout': '登出',
  'language.label': '界面语言',
  'language.zh-CN': '中文',
  'language.en': 'English',
  'shell.welcome': '欢迎使用 Engram',
  'shell.subtitle': '基于 FSRS 间隔重复算法的知识卡片系统',
  'shell.status': '系统就绪',
  'common.loading': '加载中...',
  'common.error': '出错了',
  'common.not_found': '页面未找到',
  'common.back_home': '返回首页',
  'common.retry': '重试',
  'common.empty': '暂无数据',
  'common.failed': '加载失败',
  'common.unauthorized': '未登录或会话已过期',
  'decks.title': '卡组',
  'decks.loading': '正在加载卡组...',
  'decks.empty': '暂无卡组',
  'decks.failed': '加载卡组失败',
  'decks.unauthorized': '请先登录以查看卡组',
  'decks.retry': '重试',
  'error.unauthorized': '未登录或会话已过期',
  'error.forbidden': '没有访问权限',
  'error.conflict': '数据状态冲突，请刷新后重试',
  'error.rate_limited': '请求过于频繁，请稍后重试',
  'error.not_found': '请求的资源不存在',
  'error.network': '网络连接失败，请检查网络设置',
  'error.invalid_response': '服务器响应格式异常',
  'error.unknown': '操作失败，请稍后重试',
};
