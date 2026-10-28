/**
 * Light/dark theme switching (used by the navbar button).
 *
 * **The same thing has three execution points across the site, so changing one means looking at the other two**:
 *   1. `themeBootstrapJS` in `internal/web/theme.go` — the inline script the server injects into the SPA
 *      entry's <head> at assembly time; it is responsible for setting `.dark`, `color-scheme`, the canvas
 *      background color and theme-color **before the first frame**
 *      (an external script is a separate request and inevitably loses to the first frame, so this chunk must be
 *      inline and covered by the CSP script hash allowlist).
 *   2. `isDarkTheme`/`applyTheme` in `internal/web/static/js/pwa.js` — applied once at load time,
 *      and it follows system preference changes while the user has made no choice.
 *   3. This file — the immediate switch and persistence after the user clicks the button.
 *
 * The **criteria and values of all three must be identical word for word** (storage key, `dark` class name,
 * the two canvas colors `#09090b`/`#f8fafc`), otherwise you get "one color on the first frame, another one after
 * a click". The values are pinned by `frontend/src/tests/theme.test.ts`.
 */

/** Storage key; identical to the literals in theme.go / pwa.js. */
export const THEME_STORAGE_KEY = 'engram-theme';

/** Dark canvas background color (identical to theme.go and pwa.js). */
export const DARK_CANVAS = '#09090b';

/** Light canvas background color (identical to theme.go; pwa.js sets no canvas color, only the .dark class and theme-color). */
export const LIGHT_CANVAS = '#f8fafc';

/** Value of `<meta name="theme-color">` in dark and light mode (affects the mobile browser address bar color). */
const DARK_THEME_COLOR = '#09090b';
const LIGHT_THEME_COLOR = '#ffffff';

export type ThemeChoice = 'light' | 'dark';

/**
 * Get a usable localStorage; returns null when unavailable (private mode, non-browser environment).
 *
 * It is fetched explicitly through `window` instead of using the bare global identifier: in some test
 * environments the bare `localStorage` is undefined, while `window.localStorage` is the object the host provides.
 */
function store(): Storage | null {
  try {
    if (typeof window !== 'undefined' && window.localStorage) {
      return window.localStorage;
    }
  } catch {
    // Some browsers throw on merely accessing this property in private mode.
  }
  return null;
}

/** Read the user's explicit choice; returns null when nothing is chosen or storage is unavailable. */
export function storedTheme(): ThemeChoice | null {
  try {
    const raw = store()?.getItem(THEME_STORAGE_KEY);
    return raw === 'light' || raw === 'dark' ? raw : null;
  } catch {
    return null;
  }
}

/** Whether the system prefers a dark theme. */
export function prefersDark(): boolean {
  return typeof window !== 'undefined' && !!window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
}

/**
 * Whether it should currently be dark. The criteria are **word-for-word the same shape** as theme.go's inline
 * bootstrap: an explicit choice wins, and only with none does it look at system preference.
 */
export function isDark(): boolean {
  const stored = storedTheme();
  if (stored) {
    return stored === 'dark';
  }
  return prefersDark();
}

/** Apply the theme: class name, color-scheme, canvas background color and theme-color, none omitted (omitting color-scheme makes native controls fight over their colors). */
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
 * Toggle the theme and persist it, returning the state after the toggle (true = dark).
 * Once an explicit choice is written it overrides system preference — that is the premise of "pressing the button has to count".
 */
export function toggleTheme(): boolean {
  const next = !document.documentElement.classList.contains('dark');
  try {
    store()?.setItem(THEME_STORAGE_KEY, next ? 'dark' : 'light');
  } catch {
    // When storage is unavailable the appearance still toggles for this session, it just is not remembered.
  }
  applyTheme(next);
  return next;
}
