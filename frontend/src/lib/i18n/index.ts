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
 * 语言包映射表
 */
export const catalogs: Record<SupportedLocale, LocaleCatalog> = {
  'zh-CN': zhCN,
  en,
};

/**
 * 检查语言代码是否受支持
 */
export function isSupportedLocale(code: string | null | undefined): code is SupportedLocale {
  if (!code) return false;
  return SUPPORTED_LOCALES.includes(code as SupportedLocale);
}

/**
 * 将任意语言代码匹配到最接近的受支持语言
 * 例如 'zh', 'zh-CN', 'zh-TW' -> 'zh-CN'; 'en-US', 'en' -> 'en'
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
 * 解析语言优先级（DESIGN.md §8.3）：
 * URL 参数 ?lang=<code> > 已登录用户个人设置 > Accept-Language / navigator.language > 站点默认语言
 */
export function resolveLocale(options: ResolveLocaleOptions = {}): SupportedLocale {
  const { urlParam, userSetting, navigatorLang, siteDefault = DEFAULT_LOCALE } = options;

  // 1. URL 参数显式覆盖
  if (urlParam) {
    const matched = matchSupportedLocale(urlParam);
    if (matched) return matched;
  }

  // 2. 用户个人设置
  if (userSetting) {
    const matched = matchSupportedLocale(userSetting);
    if (matched) return matched;
  }

  // 3. 浏览器语言偏好
  if (navigatorLang) {
    const matched = matchSupportedLocale(navigatorLang);
    if (matched) return matched;
  }

  // 4. 站点默认
  return siteDefault;
}

/**
 * 检测当前环境初始语言
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
 * 当前语言响应式 Store
 */
export const localeStore = writable<SupportedLocale>(detectInitialLocale());

/**
 * 切换语言
 */
export function setLocale(locale: SupportedLocale): void {
  if (isSupportedLocale(locale)) {
    localeStore.set(locale);
  }
}

/**
 * 当前语言代码
 */
export function getLocale(): SupportedLocale {
  return get(localeStore);
}

/**
 * 翻译文本函数
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
 * 响应式翻译 store 派生
 */
export const t = derived(localeStore, ($locale) => {
  return (key: string, params?: Record<string, string | number>): string => {
    return formatMessage($locale, key, params);
  };
});

export * from './types';
export * from './parity';
