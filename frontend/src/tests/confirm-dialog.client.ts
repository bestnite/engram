import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ConfirmHost from '../lib/components/ui/ConfirmHost.svelte';
import { askConfirm } from '../lib/components/ui/confirm';
import { setLocale } from '../lib/i18n';

/**
 * Contract of the unified confirm dialog: askConfirm opens the app's own dialog; clicking confirm yields true, clicking cancel yields false;
 * when another request arrives while the previous one is unanswered, the previous one ends as cancelled (the Promise never hangs forever).
 */
describe('askConfirm', () => {
  let instance: ReturnType<typeof mount> | null = null;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  function setup(): void {
    setLocale('zh-CN');
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(ConfirmHost, { target });
    flushSync();
  }

  function click(testId: string): void {
    (document.body.querySelector(`[data-testid="${testId}"]`) as HTMLElement).click();
  }

  it('resolves true when confirmed and shows the title', async () => {
    setup();
    const answer = askConfirm({ title: '确定删除用户 alice？', destructive: true });
    await flush();
    expect(document.body.textContent).toContain('确定删除用户 alice？');
    click('confirm-dialog-confirm');
    await expect(answer).resolves.toBe(true);
  });

  it('resolves false when cancelled', async () => {
    setup();
    const answer = askConfirm({ title: '确定撤销？' });
    await flush();
    click('confirm-dialog-cancel');
    await expect(answer).resolves.toBe(false);
  });

  it('settles a pending request as cancelled when a new one arrives', async () => {
    setup();
    const first = askConfirm({ title: 'first' });
    const second = askConfirm({ title: 'second' });
    await expect(first).resolves.toBe(false);
    await flush();
    click('confirm-dialog-confirm');
    await expect(second).resolves.toBe(true);
  });
});
