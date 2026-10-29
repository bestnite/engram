/**
 * The page a detour started from, carried in the address as `?return=<path>`.
 *
 * A page that sends the user somewhere to finish a side task (the review page opening the card
 * editor) appends its own address, and the target page returns there when the task ends. The value
 * travels in the address rather than in history state, so it survives a reload and a link opened in a
 * new tab, where `history.back()` would leave the app or do nothing.
 *
 * The value is user-controlled, so it is accepted only as a path inside this app that the client
 * router can render. Anything else is dropped and the target page falls back to its own default:
 * accepting `//host` or `/\host` would turn the return link into a redirect to another site.
 */
import { matchRoute } from './router';

/** The query parameter that carries the return path. */
export const RETURN_PARAM = 'return';

/** Returns the path when it is a renderable in-app path, otherwise null. */
export function safeReturnPath(raw: string | null | undefined): string | null {
  if (!raw || !raw.startsWith('/')) return null;
  // Browsers read a leading `//` or `/\` as the start of another host.
  if (raw.startsWith('//') || raw.startsWith('/\\')) return null;
  if (matchRoute(raw).route === null) return null;
  return raw;
}

/** Appends the return path to an in-app link, keeping any query the link already has. */
export function withReturn(href: string, from: string): string {
  const separator = href.includes('?') ? '&' : '?';
  return `${href}${separator}${RETURN_PARAM}=${encodeURIComponent(from)}`;
}
