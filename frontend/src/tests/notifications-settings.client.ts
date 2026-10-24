import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import NotificationPrefsView from '../lib/views/NotificationPrefsView.svelte';
import { apiClient } from '../lib/api';
import type { NotificationPrefsResponse } from '../lib/api';
import { setLocale } from '../lib/i18n';

/**
 * Saving the notification switches must not name a locked (class A) type in the request body.
 * The server refuses any submission that contains a locked type with `class_locked`, even when the
 * submitted value is `true`, so keeping the locked entries of the rendered checkbox map in the payload
 * made every save fail with "security and transactional mail cannot be turned off".
 */
describe('NotificationPrefsView saving omits locked types', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  const fixture: NotificationPrefsResponse = {
    groups: [
      {
        class: 'security',
        types: ['password_reset', 'email_verification', 'new_device_login'].map((type) => ({
          type,
          enabled: true,
          locked: true,
        })),
      },
      {
        class: 'collab',
        types: ['deck_shared', 'invite'].map((type) => ({ type, enabled: true, locked: false })),
      },
      {
        class: 'study',
        types: ['review_reminder', 'study_digest'].map((type) => ({ type, enabled: false, locked: false })),
      },
    ],
    reminder_hour: null,
    default_reminder_hour: 19,
    timezone: 'Asia/Shanghai',
  };

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

  async function setup(): Promise<ReturnType<typeof vi.fn>> {
    setLocale('zh-CN');
    const update = vi.spyOn(apiClient, 'updateNotificationPrefs').mockResolvedValue(fixture);
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(NotificationPrefsView, {
      target,
      props: { initialLoading: false, initialData: fixture },
    });
    await flush();
    return update as unknown as ReturnType<typeof vi.fn>;
  }

  function control(testId: string): HTMLElement {
    const el = document.body.querySelector(`[data-testid="${testId}"]`) as HTMLElement | null;
    expect(el, testId).not.toBeNull();
    return el!;
  }

  it('submits every closable type and never a locked one', async () => {
    const update = await setup();
    // The reported repro: nothing in the study group was selected, then every switch was turned on and saved.
    for (const type of ['review_reminder', 'study_digest']) {
      control(`notifications-type-${type}`).click();
      await flush();
    }
    control('notifications-submit').click();
    await flush();

    expect(update).toHaveBeenCalledTimes(1);
    expect(update.mock.calls[0]?.[0]).toEqual({
      choices: {
        deck_shared: true,
        invite: true,
        review_reminder: true,
        study_digest: true,
      },
      reminder_hour: null,
    });
  });

  it('omits locked types even when the user changed nothing', async () => {
    const update = await setup();
    control('notifications-submit').click();
    await flush();

    expect(update).toHaveBeenCalledTimes(1);
    const choices = (update.mock.calls[0]?.[0] as { choices: Record<string, boolean> }).choices;
    for (const type of ['password_reset', 'email_verification', 'new_device_login']) {
      expect(choices, type).not.toHaveProperty(type);
    }
  });
});
