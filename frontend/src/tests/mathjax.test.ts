import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

/**
 * SPA MathJax 加载器的单元测试（DESIGN.md §6.1、§8.5、§11）。
 *
 * 测试环境是 node（无 DOM），因此这里手搓一个最小 document/window 替身：它记录被注入的
 * <script>、在 appendChild 时捕获加载前的 window.MathJax 配置、并模拟脚本执行后把全局
 * 替换成 MathJax API 对象。模块状态用 vi.resetModules() 每例重置。
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
      // 记录 MathJax 脚本读取配置的时刻，再模拟脚本把全局换成 API 对象。
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

    // 加载前必须已写入配置：关闭整页自动排版，并保留 \(…\) / \[…\]（含 $$）分隔符。
    const cfg = configAtLoad() as {
      startup: { typeset: boolean };
      tex: { inlineMath: string[][]; displayMath: string[][] };
    };
    expect(cfg.startup.typeset).toBe(false);
    expect(cfg.tex.inlineMath).toEqual([['\\(', '\\)']]);
    expect(cfg.tex.displayMath).toEqual([['$$', '$$'], ['\\[', '\\]']]);

    // 幂等：再次调用不注入第二个脚本。
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
