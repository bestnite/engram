import { describe, it, expect, afterEach } from 'vitest';
import { initRouter } from '../lib/router';

/**
 * The global click proxy must only intercept links the client router can actually render. Server
 * endpoints such as /auth/oidc/start are full navigations: intercepting them matches no route and
 * shows the SPA 404 instead of starting the OIDC flow.
 */
describe('router link proxy', () => {
  let cleanup: (() => void) | null = null;

  afterEach(() => {
    cleanup?.();
    cleanup = null;
    document.body.innerHTML = '';
  });

  function clickLink(href: string): MouseEvent {
    cleanup = initRouter();
    const anchor = document.createElement('a');
    anchor.setAttribute('href', href);
    anchor.textContent = 'link';
    document.body.appendChild(anchor);
    const event = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 });
    anchor.dispatchEvent(event);
    return event;
  }

  it('takes over a link that matches a client route', () => {
    const event = clickLink('/settings/security');
    expect(event.defaultPrevented).toBe(true);
  });

  it('leaves a server endpoint to the browser', () => {
    const event = clickLink('/auth/oidc/start');
    expect(event.defaultPrevented).toBe(false);
  });
});
