/**
 * 支持的语言代码（DESIGN.md §8.3）
 */
export type SupportedLocale = 'zh-CN' | 'en';

export const SUPPORTED_LOCALES: readonly SupportedLocale[] = ['zh-CN', 'en'] as const;

export const DEFAULT_LOCALE: SupportedLocale = 'zh-CN';

/**
 * 语言包目录结构定义
 */
export type LocaleCatalog = Record<string, string>;
