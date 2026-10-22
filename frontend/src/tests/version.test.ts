import { describe, it, expect, afterEach, vi } from 'vitest';

/**
 * The single source for the footer's app version is the `<meta name="engram-version">` the
 * server injects into the SPA entry <head> at assembly time. The test environment is node
 * (no DOM), so a minimal document stand-in is built by hand here.
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
