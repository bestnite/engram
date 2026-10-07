// @vitest-environment happy-dom
// 主题切换会真的读写 document/localStorage，因此这个文件单独要 DOM 环境——
// 其余用例仍跑在 vitest.config.ts 默认的 node 环境里（不为一个文件改全局配置）。
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import {
  THEME_STORAGE_KEY,
  DARK_CANVAS,
  LIGHT_CANVAS,
  storedTheme,
  prefersDark,
  isDark,
  applyTheme,
  toggleTheme,
} from '../lib/theme';

/**
 * 主题切换（导航栏按钮）。
 *
 * 这一层刻意钉死取值：同一件事在全站有三个执行点——服务端首帧内联引导（internal/web/theme.go）、
 * pwa.js 的加载期应用、以及本模块。取值一旦漂移就会出现「首帧一个色、点一下变另一个色」，
 * 而那种 bug 只在真机首帧才看得见。
 */

/** 装一个可控的 localStorage 桩：DOM 环境不一定会把 Storage 暴露成全局变量。 */
function installStorage(): Storage {
  const data = new Map<string, string>();
  const stub = {
    getItem: (key: string): string | null => (data.has(key) ? (data.get(key) as string) : null),
    setItem: (key: string, value: string): void => {
      data.set(key, String(value));
    },
    removeItem: (key: string): void => {
      data.delete(key);
    },
    clear: (): void => {
      data.clear();
    },
    key: (index: number): string | null => Array.from(data.keys())[index] ?? null,
    get length(): number {
      return data.size;
    },
  } as unknown as Storage;
  Object.defineProperty(window, 'localStorage', { configurable: true, writable: true, value: stub });
  return stub;
}

function stubMatchMedia(matches: boolean): void {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  });
}

let storage: Storage;

beforeEach(() => {
  storage = installStorage();
  document.documentElement.className = '';
  document.documentElement.removeAttribute('style');
  document.head.innerHTML = '<meta name="theme-color" content="#ffffff">';
  stubMatchMedia(false);
});

afterEach(() => {
  storage.clear();
  document.documentElement.className = '';
  document.documentElement.removeAttribute('style');
});

describe('theme module', () => {
  it('pins the storage key and canvas colours that theme.go and pwa.js also use', () => {
    expect(THEME_STORAGE_KEY).toBe('engram-theme');
    expect(DARK_CANVAS).toBe('#09090b');
    expect(LIGHT_CANVAS).toBe('#f8fafc');
  });

  it('reads an explicit choice and ignores anything else', () => {
    expect(storedTheme()).toBeNull();
    storage.setItem(THEME_STORAGE_KEY, 'dark');
    expect(storedTheme()).toBe('dark');
    storage.setItem(THEME_STORAGE_KEY, 'light');
    expect(storedTheme()).toBe('light');
    storage.setItem(THEME_STORAGE_KEY, 'sepia');
    expect(storedTheme()).toBeNull();
  });

  it('falls back to the system preference only when nothing is stored', () => {
    stubMatchMedia(true);
    expect(prefersDark()).toBe(true);
    expect(isDark()).toBe(true);

    // 显式选择 light 必须压过深色的系统偏好——按钮点了要算数。
    storage.setItem(THEME_STORAGE_KEY, 'light');
    expect(isDark()).toBe(false);

    storage.setItem(THEME_STORAGE_KEY, 'dark');
    stubMatchMedia(false);
    expect(isDark()).toBe(true);
  });

  it('applies the class, color-scheme, canvas and theme-color together', () => {
    applyTheme(true);
    const root = document.documentElement;
    expect(root.classList.contains('dark')).toBe(true);
    expect(root.style.colorScheme).toBe('dark');
    expect(root.style.backgroundColor).toBe(DARK_CANVAS);
    expect(document.querySelector('meta[name="theme-color"]')?.getAttribute('content')).toBe('#09090b');

    applyTheme(false);
    expect(root.classList.contains('dark')).toBe(false);
    expect(root.style.colorScheme).toBe('light');
    expect(root.style.backgroundColor).toBe(LIGHT_CANVAS);
    expect(document.querySelector('meta[name="theme-color"]')?.getAttribute('content')).toBe('#ffffff');
  });

  it('toggles from the current DOM state, persists the choice and returns the new state', () => {
    applyTheme(false);
    expect(toggleTheme()).toBe(true);
    expect(storage.getItem(THEME_STORAGE_KEY)).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);

    expect(toggleTheme()).toBe(false);
    expect(storage.getItem(THEME_STORAGE_KEY)).toBe('light');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });

  it('still switches for this session when storage is unavailable', () => {
    vi.spyOn(storage, 'setItem').mockImplementation(() => {
      throw new Error('denied');
    });
    applyTheme(false);
    expect(toggleTheme()).toBe(true);
    expect(document.documentElement.classList.contains('dark')).toBe(true);
  });
});
