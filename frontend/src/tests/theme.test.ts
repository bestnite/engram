// @vitest-environment happy-dom
// Theme switching really reads and writes document/localStorage, so this file alone needs a DOM environment —
// the other cases still run in vitest.config.ts's default node environment (no global config change for a single file).
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
 * Theme switching (the navbar button).
 *
 * This layer deliberately pins the values: the same concern has three execution points
 * site-wide — the server's first-frame inline bootstrap (internal/web/theme.go), pwa.js's
 * apply-on-load pass, and this module. Once the values drift you get "one colour on the
 * first frame, another colour after one click", and that kind of bug is only visible on
 * the first frame of a real device.
 */

/** Install a controllable localStorage stub: the DOM environment does not necessarily expose Storage as a global. */
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

    // An explicit light choice must beat a dark system preference — clicking the button has to count.
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
