import { describe, it, expect, afterEach, vi } from 'vitest';

/**
 * 页脚程序版本的唯一来源是服务端在装配期注入 SPA 入口 <head> 的
 * `<meta name="engram-version">`。测试环境是 node（无 DOM），这里手搓一个最小 document 替身。
 */

function installDocument(metaContent: string | null) {
  const metaEl = metaContent === null ? null : { getAttribute: () => metaContent };
  vi.stubGlobal('document', {
    querySelector(selector: string) {
      return selector.includes('engram-version') ? metaEl : null;
    },
  });
}

describe('app version meta', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('reads the server-injected release tag from the meta tag', async () => {
    installDocument('v0.1.4');
    const { appVersion } = await import('../lib/version');
    expect(appVersion()).toBe('v0.1.4');
  });

  it('reads the dev build marker (dev + short revision)', async () => {
    installDocument('dev (e223664)');
    const { appVersion } = await import('../lib/version');
    expect(appVersion()).toBe('dev (e223664)');
  });

  it('returns an empty string when the meta is absent (vite dev / static preview)', async () => {
    installDocument(null);
    const { appVersion } = await import('../lib/version');
    expect(appVersion()).toBe('');
  });

  it('returns an empty string when there is no DOM', async () => {
    vi.unstubAllGlobals();
    const { appVersion } = await import('../lib/version');
    expect(appVersion()).toBe('');
  });
});
