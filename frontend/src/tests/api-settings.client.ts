import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import APIKeysView from '../lib/views/APIKeysView.svelte';
import { apiClient } from '../lib/api';
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
});
