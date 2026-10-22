/**
 * Program version.
 *
 * The server injects the version number into the SPA entry `index.html`'s <head> at assembly time:
 * `<meta name="engram-version" content="v0.1.4">`. The value comes from the tag injected at build time
 * (`-ldflags "-X main.version=<tag>"`); a local build without injection is `dev (<short commit hash>)`.
 * In dev mode (vite dev) or a static preview this meta is absent, an empty string is returned, and the footer
 * consequently shows no version.
 */

const META_NAME = 'engram-version';

/** appVersion reads the program version injected by the server; returns an empty string when it is missing. */
export function appVersion(): string {
  if (typeof document === 'undefined') return '';
  const meta = document.querySelector(`meta[name="${META_NAME}"]`);
  return (meta?.getAttribute('content') ?? '').trim();
}
