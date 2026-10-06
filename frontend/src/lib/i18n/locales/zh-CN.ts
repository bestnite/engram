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
};
