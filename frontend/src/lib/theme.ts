/**
 * 深浅主题的切换（导航栏按钮用）。
 *
 * **同一件事在全站有三个执行点，改一处必须同时看另两处**：
 *   1. `internal/web/theme.go` 的 `themeBootstrapJS`——服务端在装配期注入 SPA 入口 <head> 的
 *      内联脚本，负责**首帧之前**把 `.dark`、`color-scheme`、画布底色与 theme-color 设好
 *      （外链脚本是独立请求，必然输给首帧，所以这段必须内联、且被 CSP 的 script hash 白名单覆盖）。
 *   2. `internal/web/static/js/pwa.js` 的 `isDarkTheme`/`applyTheme`——加载期应用一次，
 *      并在用户未做选择时跟随系统偏好变化。
 *   3. 本文件——用户点击按钮后的即时切换与持久化。
 *
 * 三者的**判据与取值必须逐字一致**（存储键、`dark` 类名、`#09090b`/`#f8fafc` 两个画布色），
 * 否则会出现「首帧一个色、点一下变另一个色」。取值由 `frontend/src/tests/theme.test.ts` 钉住。
 */

/** 存储键；与 theme.go / pwa.js 里的字面量一致。 */
export const THEME_STORAGE_KEY = 'engram-theme';

/** 深色画布底色（与 theme.go、pwa.js 一致）。 */
export const DARK_CANVAS = '#09090b';

/** 浅色画布底色（与 theme.go 一致；pwa.js 不设画布色，只设 .dark 类与 theme-color）。 */
export const LIGHT_CANVAS = '#f8fafc';

/** 深色与浅色下 `<meta name="theme-color">` 的值（影响移动端浏览器地址栏配色）。 */
export const DARK_THEME_COLOR = '#09090b';
export const LIGHT_THEME_COLOR = '#ffffff';

export type ThemeChoice = 'light' | 'dark';

/**
 * 取可用的 localStorage；不可用（隐私模式、非浏览器环境）时返回 null。
 *
 * 显式经 `window` 取而不是裸用全局标识符：在部分测试环境里裸 `localStorage` 是 undefined，
 * 而 `window.localStorage` 才是宿主提供的那个对象。
 */
function store(): Storage | null {
  try {
    if (typeof window !== 'undefined' && window.localStorage) {
      return window.localStorage;
    }
  } catch {
    // 某些浏览器在隐私模式下访问该属性本身就抛错。
  }
  return null;
}

/** 读取用户显式选择；未选择或存储不可用时返回 null。 */
export function storedTheme(): ThemeChoice | null {
  try {
    const raw = store()?.getItem(THEME_STORAGE_KEY);
    return raw === 'light' || raw === 'dark' ? raw : null;
  } catch {
    return null;
  }
}

/** 系统是否偏好深色。 */
export function prefersDark(): boolean {
  return typeof window !== 'undefined' && !!window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
}

/**
 * 当前是否应当为深色。判据与 theme.go 的内联引导**逐字同形**：
 * 显式选择优先，未选择才看系统偏好。
 */
export function isDark(): boolean {
  const stored = storedTheme();
  if (stored) {
    return stored === 'dark';
  }
  return prefersDark();
}

/** 应用主题：类名、color-scheme、画布底色与 theme-color 一处不漏（漏掉 color-scheme 会让原生控件配色打架）。 */
export function applyTheme(dark: boolean): void {
  if (typeof document === 'undefined') {
    return;
  }
  const root = document.documentElement;
  root.classList.toggle('dark', dark);
  root.style.colorScheme = dark ? 'dark' : 'light';
  root.style.backgroundColor = dark ? DARK_CANVAS : LIGHT_CANVAS;
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) {
    meta.setAttribute('content', dark ? DARK_THEME_COLOR : LIGHT_THEME_COLOR);
  }
}

/**
 * 切换主题并持久化，返回切换后的状态（true＝深色）。
 * 显式选择一旦写下就会覆盖系统偏好——这是「按钮点了要算数」的前提。
 */
export function toggleTheme(): boolean {
  const next = !document.documentElement.classList.contains('dark');
  try {
    store()?.setItem(THEME_STORAGE_KEY, next ? 'dark' : 'light');
  } catch {
    // 存储不可用时仍然切换本次会话的外观，只是不再记住。
  }
  applyTheme(next);
  return next;
}
