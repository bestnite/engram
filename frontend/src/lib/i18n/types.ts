/**
 * Supported language codes
 */
export type SupportedLocale = 'zh-CN' | 'en';

export const SUPPORTED_LOCALES: readonly SupportedLocale[] = ['zh-CN', 'en'] as const;

export const DEFAULT_LOCALE: SupportedLocale = 'zh-CN';

/**
 * Language pack catalog structure definition
 */
export type LocaleCatalog = Record<string, string>;
