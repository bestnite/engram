import { describe, it, expect } from 'vitest';
import { safeReturnPath, withParam, withoutParam, withReturn } from '../lib/return-to';

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

describe('withParam and withoutParam', () => {
  it.each([
    ['/decks/5/notes/new', '/decks/5/notes/new?note_type=nt-1'],
    ['/decks/5/notes/new?return=%2Fdecks%2F5', '/decks/5/notes/new?return=%2Fdecks%2F5&note_type=nt-1'],
    ['/decks/5/notes/new?note_type=old&return=%2Fdecks%2F5', '/decks/5/notes/new?note_type=nt-1&return=%2Fdecks%2F5'],
  ])('sets the parameter on %s', (path, expected) => {
    expect(withParam(path, 'note_type', 'nt-1')).toBe(expected);
  });

  it.each([
    ['/decks/5/notes/new', '/decks/5/notes/new'],
    ['/decks/5/notes/new?note_type=nt-1', '/decks/5/notes/new'],
    ['/decks/5/notes/new?note_type=nt-1&return=%2Fdecks%2F5', '/decks/5/notes/new?return=%2Fdecks%2F5'],
  ])('removes the parameter from %s', (path, expected) => {
    expect(withoutParam(path, 'note_type')).toBe(expected);
  });

  it('keeps a nested return path decodable after a round trip', () => {
    const editor = withReturn('/decks/5/notes/new', '/review?deck=5&tag=a b');
    const back = withParam(editor, 'note_type', 'nt-1');
    expect(new URLSearchParams(back.split('?')[1]).get('return')).toBe('/review?deck=5&tag=a b');
  });
});
