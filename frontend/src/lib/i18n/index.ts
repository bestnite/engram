import { writable, derived, get } from 'svelte/store';
import {
  type SupportedLocale,
  type LocaleCatalog,
  SUPPORTED_LOCALES,
  DEFAULT_LOCALE,
} from './types';
import { zhCN } from './locales/zh-CN';
import { en } from './locales/en';

/**
 * Language pack map
 */
export const catalogs: Record<SupportedLocale, LocaleCatalog> = {
  'zh-CN': zhCN,
  en,
};

/**
 * Check whether a language code is supported
 */
export function isSupportedLocale(code: string | null | undefined): code is SupportedLocale {
  if (!code) return false;
  return SUPPORTED_LOCALES.includes(code as SupportedLocale);
}

/**
 * Match any language code to the nearest supported language
 * e.g. 'zh', 'zh-CN', 'zh-TW' -> 'zh-CN'; 'en-US', 'en' -> 'en'
 */
export function matchSupportedLocale(lang: string | null | undefined): SupportedLocale | null {
  if (!lang) return null;
  const normalized = lang.trim().toLowerCase();
  if (normalized.startsWith('zh')) return 'zh-CN';
  if (normalized.startsWith('en')) return 'en';
  return null;
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
export function detectInitialLocale(): SupportedLocale {
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
    for (const [k, v] of Object.entries(params)) {
      text = text.replace(new RegExp(`\\{${k}\\}`, 'g'), String(v));
    }
  }

  return text;
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
