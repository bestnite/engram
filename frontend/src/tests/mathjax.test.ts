import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

/**
 * Unit tests for the SPA MathJax loader.
 *
 * The test environment is node (no DOM), so a minimal document/window stand-in is built by
 * hand here: it records the injected <script>, captures the pre-load window.MathJax config
 * at appendChild time, and simulates the script replacing the global with the MathJax API
 * object. Module state is reset per case with vi.resetModules().
 */

interface FakeScript {
  src: string;
  async: boolean;
  onload: (() => void) | null;
  onerror: (() => void) | null;
}

function installDocument(metaContent: string | null, fakeApi: Record<string, unknown>) {
  const scripts: FakeScript[] = [];
  let configAtLoad: unknown = null;
  const head = {
    appendChild(el: FakeScript) {
      scripts.push(el);
      // Record the moment the MathJax script reads its config, then simulate the script swapping the global for the API object.
      configAtLoad = (globalThis as { MathJax?: unknown }).MathJax;
      (globalThis as { MathJax?: unknown }).MathJax = fakeApi;
      el.onload?.();
      return el;
    },
  };
  const metaEl = metaContent === null ? null : { getAttribute: () => metaContent };
  const doc = {
    head,
    defaultView: globalThis,
    querySelector(selector: string) {
      return selector.includes('engram-mathjax') ? metaEl : null;
    },
    createElement(): FakeScript {
      return { src: '', async: false, onload: null, onerror: null };
    },
  };
  vi.stubGlobal('document', doc);
  return { scripts, configAtLoad: () => configAtLoad };
}

describe('SPA MathJax loader', () => {
  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    delete (globalThis as { MathJax?: unknown }).MathJax;
  });

  it('reads the server-injected MathJax URL from the meta tag', async () => {
    installDocument('/static/v/abcd1234/js/mathjax/tex-svg.js', {});
    const mj = await import('../lib/mathjax');
    expect(mj.mathjaxURL()).toBe('/static/v/abcd1234/js/mathjax/tex-svg.js');
  });

  it('rejects non-relative meta content so an external CDN can never be loaded', async () => {
    installDocument('https://cdn.example.com/mathjax/tex-svg.js', {});
    const mj = await import('../lib/mathjax');
    expect(mj.mathjaxURL()).toBe('');
    const api = await mj.ensureMathJax();
    expect(api).toBeNull();
  });

  it('injects one same-origin script and sets the MathJax config before it loads', async () => {
    const url = '/static/v/abcd1234/js/mathjax/tex-svg.js';
    const fakeApi = { typesetPromise: vi.fn(async () => {}), typesetClear: vi.fn() };
    const { scripts, configAtLoad } = installDocument(url, fakeApi);
    const mj = await import('../lib/mathjax');

    const api = await mj.ensureMathJax();
    expect(api).toBe(fakeApi);
    expect(scripts).toHaveLength(1);
    expect(scripts[0]!.src).toBe(url);

    // The config must already be written before loading: disable whole-page typesetting and keep \(…\) / \[…\] (including $$) delimiters.
    const cfg = configAtLoad() as {
      startup: { typeset: boolean };
      tex: { inlineMath: string[][]; displayMath: string[][] };
    };
    expect(cfg.startup.typeset).toBe(false);
    expect(cfg.tex.inlineMath).toEqual([['\\(', '\\)']]);
    expect(cfg.tex.displayMath).toEqual([['$$', '$$'], ['\\[', '\\]']]);

    // Idempotent: a second call does not inject a second script.
    await mj.ensureMathJax();
    expect(scripts).toHaveLength(1);
  });

  it('no-ops (no script) when the server injected no URL', async () => {
    const { scripts } = installDocument(null, {});
    const mj = await import('../lib/mathjax');
    expect(mj.mathjaxURL()).toBe('');
    expect(await mj.ensureMathJax()).toBeNull();
    expect(scripts).toHaveLength(0);
  });

  it('typesets only the given containers, clearing stale MathJax DOM first', async () => {
    const fakeApi = { typesetPromise: vi.fn(async () => {}), typesetClear: vi.fn() };
    installDocument('/static/v/abcd1234/js/mathjax/tex-svg.js', fakeApi);
    const mj = await import('../lib/mathjax');

    const a = {} as Element;
    const b = {} as Element;
    await mj.typeset([a, null, undefined, b]);

    expect(fakeApi.typesetClear).toHaveBeenCalledTimes(1);
    expect(fakeApi.typesetClear).toHaveBeenCalledWith([a, b]);
    expect(fakeApi.typesetPromise).toHaveBeenCalledTimes(1);
    expect(fakeApi.typesetPromise).toHaveBeenCalledWith([a, b]);
  });

  it('never throws when MathJax is unavailable or typesetting fails', async () => {
    const fakeApi = {
      typesetClear: vi.fn(),
      typesetPromise: vi.fn(async () => {
        throw new Error('typeset failed');
      }),
    };
    installDocument('/static/v/abcd1234/js/mathjax/tex-svg.js', fakeApi);
    const mj = await import('../lib/mathjax');
    await expect(mj.typeset([{} as Element])).resolves.toBeUndefined();
  });
});

/**
 * Tailwind's preflight sets svg to display: block, so every inline formula in MathJax's SVG
 * output takes a line of its own. app.css must restore svgs inside mjx-container to an
 * inline element; delete that rule and inline formulas on the review page, the editor
 * preview and the share page go back to one per line.
 */
describe('MathJax SVG layout', () => {
  it('keeps the MathJax svg inline despite the Tailwind preflight', async () => {
    const fs = await import('node:fs');
    const { fileURLToPath } = await import('node:url');
    const css = fs.readFileSync(fileURLToPath(new URL('../app.css', import.meta.url)), 'utf-8');
    expect(css).toMatch(/mjx-container\[jax='SVG'\]\s*>\s*svg\s*\{\s*display:\s*inline;\s*\}/);
  });
});
