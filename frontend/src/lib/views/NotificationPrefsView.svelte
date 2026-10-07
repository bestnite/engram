<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { NotificationPrefsResponse, UpdateNotificationPrefsRequest } from '../api';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialData?: NotificationPrefsResponse | null;
  }

  let { initialLoading = true, initialError = null, initialData = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let data = $state<NotificationPrefsResponse | null>(initialData);
  // 勾选状态按类型 id 存；未在 map 里的键按关闭处理，与服务端一致。
  // svelte-ignore state_referenced_locally
  let choices = $state<Record<string, boolean>>(initialData ? choiceMap(initialData) : {});
  // 发送时间下拉的值：'' = 站点默认（null），其余是 '0'–'23'。
  // svelte-ignore state_referenced_locally
  let reminderHour = $state<string>(initialData ? hourValue(initialData.reminder_hour) : '');

  let saving = $state(false);
  let actionError = $state('');
  let notice = $state('');

  const hours = Array.from({ length: 24 }, (_, h) => h);

  /** 把响应里的类型开关摊平成 {type: enabled} 的勾选 map。 */
  function choiceMap(resp: NotificationPrefsResponse): Record<string, boolean> {
    const out: Record<string, boolean> = {};
    for (const group of resp.groups) {
      for (const item of group.types) {
        out[item.type] = item.enabled;
      }
    }
    return out;
  }

  /** 发送小时转下拉值：null = 站点默认（空串），0–23 转成十进制字符串。 */
  function hourValue(hour: number | null): string {
    return hour === null || hour === undefined ? '' : String(hour);
  }

  /** 用服务端响应覆盖本地状态，保证界面只反映服务端确认过的值。 */
  function applyData(resp: NotificationPrefsResponse): void {
    data = resp;
    choices = choiceMap(resp);
    reminderHour = hourValue(resp.reminder_hour);
  }

  /** 读取偏好；未登录由服务端 401 决定，前端不猜测。 */
  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      applyData(await apiClient.getNotificationPrefs());
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && loadError.isUnauthorized) return 'error.unauthorized';
    return 'settings.notifications.failed';
  }

  /** 稳定错误 code → 本地化 key；未知 code 回落到通用失败提示。 */
  function actionErrorKey(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      unknown_type: 'settings.notifications.error.unknown_type',
      class_locked: 'settings.notifications.error.class_locked',
      reminder_hour_invalid: 'settings.notifications.error.reminder_hour_invalid',
      invalid_request: 'settings.notifications.error.invalid_request',
    };
    return map[code] || 'settings.notifications.error.failed';
  }

  function toggle(type: string, event: Event): void {
    const checked = (event.target as HTMLInputElement).checked;
    choices = { ...choices, [type]: checked };
  }

  /** 保存：为每个可关闭类型提交显式开关，发送时间为 null（站点默认）或 0–23。 */
  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    actionError = '';
    notice = '';
    saving = true;
    const payload: UpdateNotificationPrefsRequest = {
      choices,
      reminder_hour: reminderHour === '' ? null : Number(reminderHour),
    };
    try {
      applyData(await apiClient.updateNotificationPrefs(payload));
      notice = 'settings.notifications.saved';
    } catch (err) {
      actionError = actionErrorKey(err);
    } finally {
      saving = false;
    }
  }

  onMount(() => {
    if (initialData === null && initialError === null) {
      load();
    }
  });
</script>

<div class="mx-auto max-w-4xl space-y-6 px-4 py-10" data-testid="notifications-view">
  <a
    href="/settings"
    data-testid="notifications-back"
    class="inline-block text-sm font-medium text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors"
  >
    {$t('nav.settings')}
  </a>

  {#if loading}
    <div data-testid="notifications-loading" class="py-12 text-center text-zinc-500 dark:text-zinc-400">
      <div class="inline-block animate-spin w-6 h-6 border-2 border-current border-t-transparent rounded-full mb-3" aria-hidden="true"></div>
      <p class="text-sm">{$t('common.loading')}</p>
    </div>
  {:else if loadError}
    <div data-testid="notifications-failed" class="card-elevated p-8 rounded-xl text-center">
      <p role="alert" class="text-base font-medium text-zinc-900 dark:text-zinc-100">{$t(loadErrorKey())}</p>
      <button
        type="button"
        data-testid="notifications-retry"
        class="mt-4 px-4 py-2 text-sm font-medium rounded-lg bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors cursor-pointer"
        onclick={() => load()}
      >
        {$t('common.retry')}
      </button>
    </div>
  {:else if data}
    <header class="space-y-1">
      <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100" data-testid="notifications-title">
        {$t('settings.notifications.heading')}
      </h1>
      <p class="text-sm text-zinc-600 dark:text-zinc-400 leading-relaxed">{$t('settings.notifications.intro')}</p>
    </header>

    {#if actionError}
      <div data-testid="notifications-action-error" role="alert" class="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300">
        {$t(actionError)}
      </div>
    {/if}
    {#if notice}
      <div data-testid="notifications-notice" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
        {$t(notice)}
      </div>
    {/if}

    <form onsubmit={save} class="space-y-6">
      {#each data.groups as group (group.class)}
        <section class="card-elevated p-6 rounded-xl space-y-4" data-testid="notifications-group-{group.class}">
          <h2 class="text-lg font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
            {$t('settings.notifications.class.' + group.class + '.heading')}
          </h2>
          <ul class="divide-y divide-zinc-100 dark:divide-zinc-800">
            {#each group.types as item (item.type)}
              <li class="py-3.5 flex items-start gap-3.5 first:pt-0 last:pb-0">
                {#if item.locked}
                  <input
                    data-testid="notifications-locked-{item.type}"
                    class="mt-1 h-4 w-4 rounded border-zinc-300 text-zinc-400 bg-zinc-100 dark:border-zinc-700 dark:bg-zinc-800 cursor-not-allowed"
                    type="checkbox"
                    checked
                    disabled
                  />
                {:else}
                  <input
                    data-testid="notifications-type-{item.type}"
                    class="mt-1 h-4 w-4 rounded border-zinc-300 text-zinc-950 focus:ring-zinc-900 dark:border-zinc-700 dark:bg-zinc-900 dark:focus:ring-zinc-100 cursor-pointer"
                    type="checkbox"
                    checked={choices[item.type] ?? false}
                    onchange={(event) => toggle(item.type, event)}
                  />
                {/if}
                <div class="space-y-0.5">
                  <span class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">{$t('settings.notifications.type.' + item.type)}</span>
                  {#if item.locked}
                    <p class="text-xs text-zinc-400 dark:text-zinc-500">{$t('settings.notifications.locked')}</p>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
        </section>
      {/each}

      <section class="card-elevated p-6 rounded-xl space-y-4" data-testid="notifications-reminder-section">
        <h2 class="text-lg font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
          {$t('settings.notifications.reminder.heading')}
        </h2>
        <label class="block">
          <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('settings.notifications.reminder.label')}</span>
          <select
            data-testid="notifications-reminder"
            bind:value={reminderHour}
            class="mt-1.5 w-full rounded-xl border border-zinc-200 bg-white px-3.5 py-2.5 text-sm text-zinc-900 shadow-2xs focus:border-zinc-900 focus:outline-none focus:ring-1 focus:ring-zinc-900 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:focus:border-zinc-100 dark:focus:ring-zinc-100 transition-colors cursor-pointer"
          >
            <option value="">{$t('settings.notifications.reminder.default', { hour: data.default_reminder_hour })}</option>
            {#each hours as hour}
              <option value={String(hour)}>{String(hour)}:00</option>
            {/each}
          </select>
          <p class="mt-1 text-xs text-zinc-400 dark:text-zinc-500">
            {$t('settings.notifications.reminder.hint', { tz: data.timezone })}
          </p>
        </label>
      </section>

      <div class="pt-2">
        <button
          type="submit"
          data-testid="notifications-submit"
          disabled={saving}
          class="inline-flex items-center justify-center rounded-xl bg-zinc-950 px-5 py-2.5 text-sm font-semibold text-white shadow-xs hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-white active:scale-[0.98] transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {$t(saving ? 'settings.notifications.saving' : 'settings.notifications.submit')}
        </button>
      </div>
    </form>
  {/if}
</div>
