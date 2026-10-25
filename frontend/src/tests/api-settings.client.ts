import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import APIKeysView from '../lib/views/APIKeysView.svelte';
import { apiClient, type APIKeyRecord } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * API and MCP page: the access address is taken from the site address the browser is currently visiting; the MCP config snippet is normally a placeholder,
 * and right after a key is created it carries that key directly (it is shown only this once).
 */
describe('API & MCP settings page', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  function text(testId: string): string {
    return target.querySelector(`[data-testid="${testId}"]`)?.textContent ?? '';
  }

  async function setup(): Promise<void> {
    setLocale('zh-CN');
    vi.spyOn(apiClient, 'getAPIKeys').mockResolvedValue({ keys: [] });
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(APIKeysView, { target });
    await flush();
  }

  function createSpy() {
    return vi.spyOn(apiClient, 'createAPIKey').mockResolvedValue({
      key: { id: '1', name: 'agent', prefix: 'fcard_ab', scopes: 'read', created_at: '', last_used_at: null, expires_at: null, revoked_at: null } as never,
      plaintext: 'fcard_secret',
    });
  }

  async function fillName(value = 'agent'): Promise<void> {
    const nameInput = target.querySelector('input[maxlength="100"]') as HTMLInputElement;
    nameInput.value = value;
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
  }

  async function submit(): Promise<void> {
    (target.querySelector('[data-testid="keys-create"]') as HTMLButtonElement).closest('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flush();
  }

  /** Opens the expiry dropdown and clicks the option carrying this copy. */
  async function chooseExpiry(label: string): Promise<void> {
    const trigger = target.querySelector('[data-testid="keys-expiry-preset"]') as HTMLElement;
    trigger.click();
    await flush();
    const option = Array.from(document.body.querySelectorAll('[role="option"]')).find((el) => el.textContent?.trim() === label);
    expect(option, `option ${label}`).toBeTruthy();
    // The select commits on pointerup (a click alone is ignored by the component), so the test dispatches the same event a mouse release sends.
    option!.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerType: 'mouse' }));
    await flush();
  }

  async function setCustomDate(value: string): Promise<void> {
    const input = target.querySelector('[data-testid="keys-expiry-date"]') as HTMLInputElement;
    expect(input, 'custom date input').not.toBeNull();
    input.value = value;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
  }

  it('shows the MCP and REST endpoints on the current origin', async () => {
    await setup();
    expect(text('api-mcp-url')).toBe(`${window.location.origin}/mcp`);
    expect(text('api-rest-url')).toBe(`${window.location.origin}/api/v1`);
  });

  it('puts a placeholder in the client config until a key is created', async () => {
    await setup();
    const config = JSON.parse(text('api-mcp-config'));
    expect(config.mcpServers.engram.url).toBe(`${window.location.origin}/mcp`);
    expect(config.mcpServers.engram.headers.Authorization).toBe('Bearer YOUR_API_KEY');
  });

  it('fills the new key into the client config right after creating it', async () => {
    await setup();
    vi.spyOn(apiClient, 'createAPIKey').mockResolvedValue({
      key: { id: '1', name: 'agent', prefix: 'fcard_ab', scopes: ['read'], created_at: '', last_used_at: null, expires_at: null, revoked_at: null } as never,
      plaintext: 'fcard_secret',
    });
    const nameInput = target.querySelector('input[maxlength="100"]') as HTMLInputElement;
    nameInput.value = 'agent';
    nameInput.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    (target.querySelector('[data-testid="keys-create"]') as HTMLButtonElement).closest('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flush();
    expect(JSON.parse(text('api-mcp-config')).mcpServers.engram.headers.Authorization).toBe('Bearer fcard_secret');
  });

  it('sends the chosen preset as an ISO instant ahead of now', async () => {
    await setup();
    const create = createSpy();
    await fillName();
    await chooseExpiry('30 天后');
    const before = Date.now();
    await submit();
    const after = Date.now();

    const payload = create.mock.calls[0]![0] as { expires_at?: string };
    expect(payload.expires_at).toBeTruthy();
    const expiresAt = new Date(payload.expires_at!).getTime();
    expect(expiresAt).toBeGreaterThanOrEqual(before + 30 * 24 * 60 * 60 * 1000);
    expect(expiresAt).toBeLessThanOrEqual(after + 30 * 24 * 60 * 60 * 1000);
  });

  it('omits the expiry field when "never" is chosen', async () => {
    await setup();
    const create = createSpy();
    await fillName();
    await submit();
    expect(create.mock.calls[0]![0]).toEqual({ name: 'agent', scopes: ['read'], expires_at: undefined });
  });

  it('sends the end of the chosen local day for a custom date', async () => {
    await setup();
    const create = createSpy();
    await fillName();
    await chooseExpiry('指定日期');
    await setCustomDate('2099-05-04');
    await submit();

    const payload = create.mock.calls[0]![0] as { expires_at?: string };
    expect(payload.expires_at).toBe(new Date(2099, 4, 4, 23, 59, 59, 0).toISOString());
  });

  it('refuses a custom date that is not in the future and creates nothing', async () => {
    await setup();
    const create = createSpy();
    await fillName();
    await chooseExpiry('指定日期');
    await setCustomDate('2000-01-01');
    await submit();

    expect(create).not.toHaveBeenCalled();
    expect(text('keys-expiry-error')).toBe('请选择将来的日期。');
  });

  it('refuses to submit a custom date without a date', async () => {
    await setup();
    const create = createSpy();
    await fillName();
    await chooseExpiry('指定日期');
    await submit();

    expect(create).not.toHaveBeenCalled();
    expect(text('keys-expiry-error')).toBe('请选择过期日期。');
  });
});

describe('API key expiry in the personal list', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  function key(overrides: Partial<APIKeyRecord> & { id: string }): APIKeyRecord {
    return {
      name: `key-${overrides.id}`,
      prefix: 'fcard_ab',
      scopes: 'read',
      created_at: '',
      expires_at: null,
      last_used_at: null,
      revoked_at: null,
      ...overrides,
    };
  }

  async function mountWith(keys: APIKeyRecord[]): Promise<void> {
    setLocale('zh-CN');
    vi.spyOn(apiClient, 'getAPIKeys').mockResolvedValue({ keys });
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(APIKeysView, { target });
    await flush();
  }

  const past = new Date(Date.now() - 60 * 60 * 1000).toISOString();
  const future = new Date(Date.now() + 60 * 60 * 1000).toISOString();

  it('separates a past expiry from the usable keys and marks it expired', async () => {
    await mountWith([
      key({ id: 'active', expires_at: future }),
      key({ id: 'expired', name: 'old-agent', expires_at: past }),
      key({ id: 'forever', name: 'forever-agent' }),
    ]);

    expect(target.querySelector('[data-testid="keys-expired-badge-expired"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="keys-revoked-badge-expired"]')).toBeNull();
    // The expired key is not offered for revocation; only the usable keys are.
    expect(target.querySelector('[data-testid="keys-revoke-expired"]')).toBeNull();
    expect(target.querySelector('[data-testid="keys-revoke-active"]')).not.toBeNull();

    const html = target.textContent ?? '';
    expect(html).toContain('已过期的密钥');
    expect(html).toContain('永不过期');
    const expiredDate = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium' }).format(new Date(past));
    expect(html).toContain(`已于 ${expiredDate} 过期`);
  });

  it('lets revoked win over expired, listing such a key only once', async () => {
    await mountWith([key({ id: 'both', expires_at: past, revoked_at: past })]);

    expect(target.querySelector('[data-testid="keys-revoked-badge-both"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="keys-expired-badge-both"]')).toBeNull();
    expect(target.textContent).not.toContain('已过期的密钥');
  });
});
