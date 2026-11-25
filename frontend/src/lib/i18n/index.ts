import { writable, derived, get } from 'svelte/store';
import {
  type SupportedLocale,
  type LocaleCatalog,
  SUPPORTED_LOCALES,
  DEFAULT_LOCALE,
} from './types';
import { zhCN } from './locales/zh-CN';
import { zhTW } from './locales/zh-TW';
import { en } from './locales/en';
import { ja } from './locales/ja';
import { ko } from './locales/ko';
import { es } from './locales/es';
import { fr } from './locales/fr';
import { de } from './locales/de';
import { ptBR } from './locales/pt-BR';
import { ru } from './locales/ru';

/**
 * Language pack map
 */
const catalogs: Record<SupportedLocale, LocaleCatalog> = {
  'zh-CN': zhCN,
  'zh-TW': zhTW,
  en,
  ja,
  ko,
  es,
  fr,
  de,
  'pt-BR': ptBR,
  ru,
};

/**
 * Check whether a language code is supported
 */
export function isSupportedLocale(code: string | null | undefined): code is SupportedLocale {
  if (!code) return false;
  return SUPPORTED_LOCALES.includes(code as SupportedLocale);
}

/**
 * Match any language code to the nearest supported language.
 *
 * An exact code wins. Otherwise the base language decides, and Chinese — the one base with two
 * catalogs — is decided by script and region: Traditional script and the Taiwan, Hong Kong and
 * Macau regions select zh-TW, every other Chinese tag selects zh-CN. Portuguese always lands on
 * pt-BR, the only Portuguese catalog shipped.
 * e.g. 'zh-TW' and 'zh-Hant' -> zh-TW; 'zh', 'zh-CN', 'zh-SG' -> zh-CN; 'pt-PT' -> pt-BR;
 * 'en-US' -> en; 'fr-CA' -> fr.
 */
function matchSupportedLocale(lang: string | null | undefined): SupportedLocale | null {
  if (!lang) return null;
  const normalized = lang.trim().toLowerCase().replace(/_/g, '-');
  if (!normalized) return null;
  const exact = SUPPORTED_LOCALES.find((code) => code.toLowerCase() === normalized);
  if (exact) return exact;
  const [base, ...subtags] = normalized.split('-');
  if (base === 'zh') {
    const traditional =
      subtags.includes('hant') ||
      subtags.includes('tw') ||
      subtags.includes('hk') ||
      subtags.includes('mo');
    return traditional ? 'zh-TW' : 'zh-CN';
  }
  return SUPPORTED_LOCALES.find((code) => code.toLowerCase().split('-')[0] === base) ?? null;
}

export interface ResolveLocaleOptions {
  urlParam?: string | null;
  userSetting?: string | null;
  navigatorLang?: string | null;
  siteDefault?: SupportedLocale;
}

/**
 * Resolve language priority:
 * URL param ?lang=<code> > logged-in user's profile settings > Accept-Language / navigator.language > site default language
 */
export function resolveLocale(options: ResolveLocaleOptions = {}): SupportedLocale {
  const { urlParam, userSetting, navigatorLang, siteDefault = DEFAULT_LOCALE } = options;

  // 1. Explicit override via the URL parameter
  if (urlParam) {
    const matched = matchSupportedLocale(urlParam);
    if (matched) return matched;
  }

  // 2. User profile settings
  if (userSetting) {
    const matched = matchSupportedLocale(userSetting);
    if (matched) return matched;
  }

  // 3. Browser language preference
  if (navigatorLang) {
    const matched = matchSupportedLocale(navigatorLang);
    if (matched) return matched;
  }

  // 4. Site default
  return siteDefault;
}

/**
 * Detect the initial language for the current environment
 */
function detectInitialLocale(): SupportedLocale {
  let urlParam: string | null = null;
  let navLang: string | null = null;

  if (typeof window !== 'undefined' && window.location) {
    const params = new URLSearchParams(window.location.search);
    urlParam = params.get('lang');
  }

  if (typeof navigator !== 'undefined') {
    navLang = navigator.language;
  }

  return resolveLocale({
    urlParam,
    navigatorLang: navLang,
  });
}

/**
 * Reactive store for the current language
 */
export const localeStore = writable<SupportedLocale>(detectInitialLocale());

/**
 * Switch language
 */
export function setLocale(locale: SupportedLocale): void {
  if (isSupportedLocale(locale)) {
    localeStore.set(locale);
  }
}

/**
 * Current language code
 */
export function getLocale(): SupportedLocale {
  return get(localeStore);
}

/**
 * Translation text function
 */
export function formatMessage(
  locale: SupportedLocale,
  key: string,
  params?: Record<string, string | number>
): string {
  const catalog = catalogs[locale] || catalogs[DEFAULT_LOCALE];
  let text = catalog[key] || catalogs[DEFAULT_LOCALE][key] || key;

  if (params) {
    // A count-driven key carries one entry per plural category this language selects
    // (`.one`, `.few`, `.many`, ...; see PLURAL_BASE_KEYS). The category comes from the
    // locale's own plural rule, and a category without an entry falls back to the base
    // text, so a language that never selects it needs no variant.
    const count = pluralCount(params);
    if (count !== null) {
      const category = new Intl.PluralRules(locale).select(count);
      text = catalog[`${key}.${category}`] || text;
    }
    for (const [k, v] of Object.entries(params)) {
      // A function replacement keeps `$&`, `$'`, `` $` `` and `$$` in the value literal: a string
      // replacement would expand them as replacement patterns and corrupt the message.
      text = text.replace(new RegExp(`\\{${k}\\}`, 'g'), () => String(v));
    }
  }

  return text;
}

/**
 * Returns the value that selects a plural variant, or null when the message is
 * not count-driven. `count`, `total` and `days` are the parameter names the
 * catalogs use for a number that changes a noun.
 */
function pluralCount(params: Record<string, string | number>): number | null {
  for (const name of ['count', 'total', 'days']) {
    const value = params[name];
    if (typeof value === 'number' && Number.isFinite(value)) return value;
  }
  return null;
}

/**
 * Reactive translation store derived value
 */
export const t = derived(localeStore, ($locale) => {
  return (key: string, params?: Record<string, string | number>): string => {
    return formatMessage($locale, key, params);
  };
});

export * from './types';
export * from './parity';
export * from './plural';
