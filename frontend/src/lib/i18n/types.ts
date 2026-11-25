/**
 * Supported language codes
 */
export type SupportedLocale =
  | 'zh-CN'
  | 'zh-TW'
  | 'en'
  | 'ja'
  | 'ko'
  | 'es'
  | 'fr'
  | 'de'
  | 'pt-BR'
  | 'ru';

/**
 * Every shipped language, in the order the language switcher shows them.
 *
 * Adding a language = add `locales/<code>.ts`, import it in `index.ts`, register it in the catalog
 * map and append the code here. The label comes from `language.<code>` in every catalog.
 */
export const SUPPORTED_LOCALES: readonly SupportedLocale[] = [
  'zh-CN',
  'zh-TW',
  'en',
  'ja',
  'ko',
  'es',
  'fr',
  'de',
  'pt-BR',
  'ru',
] as const;

export const DEFAULT_LOCALE: SupportedLocale = 'zh-CN';

/**
 * The language answered when the browser states a preference no shipped catalog covers. The site default is reserved
 * for visitors who state no preference at all.
 */
export const FALLBACK_LOCALE: SupportedLocale = 'en';

/**
 * Language pack catalog structure definition
 */
export type LocaleCatalog = Record<string, string>;
