import type { LocaleCatalog } from '../types';

/**
 * 英文语言包骨架（DESIGN.md §8.3、§10.4）
 * key 一律英文小写点分。与 zh-CN.ts 的 key 集合必须完全一致。
 */
export const en: LocaleCatalog = {
  'app.name': 'Engram',
  'nav.today': 'Today',
  'nav.decks': 'Decks',
  'nav.stats': 'Stats',
  'nav.settings': 'Settings',
  'nav.login': 'Log In',
  'nav.logout': 'Log Out',
  'language.label': 'Language',
  'language.zh-CN': '中文',
  'language.en': 'English',
  'shell.welcome': 'Welcome to Engram',
  'shell.subtitle': 'Spaced repetition flashcard system powered by FSRS',
  'shell.status': 'System Ready',
  'common.loading': 'Loading...',
  'common.error': 'Error occurred',
  'common.not_found': 'Page Not Found',
  'common.back_home': 'Back to Home',
  'common.retry': 'Retry',
  'common.empty': 'No data',
  'common.failed': 'Failed to load',
  'common.unauthorized': 'Authentication required',
  'decks.title': 'Decks',
  'decks.loading': 'Loading decks...',
  'decks.empty': 'No decks found',
  'decks.failed': 'Failed to load decks',
  'decks.unauthorized': 'Please sign in to view decks',
  'decks.retry': 'Retry',
  'error.unauthorized': 'Authentication required',
  'error.forbidden': 'Access denied',
  'error.conflict': 'Conflict with current state, please refresh',
  'error.rate_limited': 'Too many requests, please try again later',
  'error.not_found': 'Resource not found',
  'error.network': 'Network error, please check connection',
  'error.invalid_response': 'Invalid server response',
  'error.unknown': 'Operation failed, please try again',
};
