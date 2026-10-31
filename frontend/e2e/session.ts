import { expect, type Page } from '@playwright/test';

/**
 * The signed-in account the app-level browser cases share. The global setup gives a run one fresh database, and setup
 * creates its first account once: the first case to sign in creates it, every later case only logs in.
 */
const USERNAME = 'e2e-user';
const PASSWORD = 'e2e-password-CHANGE_ME';

/** Calls the JSON API from the page, so the session cookie and the CSRF token are the browser's own. */
export async function api(page: Page, method: string, path: string, body?: unknown): Promise<{ status: number; json: unknown }> {
  return page.evaluate(
    async ({ method, path, body }) => {
      const session = await (await fetch('/api/v1/auth/session')).json();
      const res = await fetch(path, {
        method,
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': session.csrf_token ?? '' },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const text = await res.text();
      return { status: res.status, json: text ? JSON.parse(text) : null };
    },
    { method, path, body },
  );
}

/** Opens the app and signs in, creating the shared account on the run's first call. */
export async function signIn(page: Page): Promise<void> {
  await page.goto('/');
  const setup = await api(page, 'POST', '/api/v1/auth/setup', { username: USERNAME, email: 'e2e@example.com', password: PASSWORD });
  // 200 creates the account; a later call is refused because the instance is already set up.
  expect(setup.status === 200 || setup.status >= 400, JSON.stringify(setup.json)).toBe(true);
  const login = await api(page, 'POST', '/api/v1/auth/login', { username: USERNAME, password: PASSWORD });
  expect(login.status, JSON.stringify(login.json)).toBe(200);
}
