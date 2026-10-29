import { describe, it, expect } from 'vitest';
import { safeReturnPath, withReturn } from '../lib/return-to';

/**
 * The return path comes from the address, so anyone can write it. Only a path this app's router can
 * render is accepted; every form a browser reads as another host, and every server-only path, is
 * dropped so the caller falls back to its own default.
 */
describe('safeReturnPath', () => {
  it.each([
    ['/review', '/review'],
    ['/review?deck=5&tag=verbs', '/review?deck=5&tag=verbs'],
    ['/decks/5', '/decks/5'],
  ])('accepts the in-app path %s', (raw, expected) => {
    expect(safeReturnPath(raw)).toBe(expected);
  });

  it.each([
    ['an empty value', ''],
    ['a missing value', undefined],
    ['a relative path', 'review'],
    ['a protocol-relative host', '//example.com/review'],
    ['a backslash host', '/\\example.com'],
    ['an absolute URL', 'https://example.com/review'],
    ['a script URL', 'javascript:alert(1)'],
    ['a server-only path', '/api/v1/notes'],
  ])('rejects %s', (_name, raw) => {
    expect(safeReturnPath(raw)).toBeNull();
  });
});

describe('withReturn', () => {
  it('appends the encoded return path to a link without a query', () => {
    expect(withReturn('/decks/2/notes/7', '/review?deck=2&tag=a')).toBe(
      '/decks/2/notes/7?return=%2Freview%3Fdeck%3D2%26tag%3Da'
    );
  });

  it('keeps a query the link already has', () => {
    expect(withReturn('/decks/2/notes/7?x=1', '/review')).toBe('/decks/2/notes/7?x=1&return=%2Freview');
  });
});
