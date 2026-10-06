<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { TOTPStatus } from '../api';

  interface Props {
    initialLoading?: boolean;
    initialError?: ApiClientError | Error | null;
    initialStatus?: TOTPStatus | null;
    initialSecret?: string;
    initialOtpauthUrl?: string;
    initialRecoveryCodes?: string[];
  }

  let {
    initialLoading = true,
    initialError = null,
    initialStatus = null,
    initialSecret = '',
    initialOtpauthUrl = '',
    initialRecoveryCodes = [],
  }: Props = $props();

  // svelte-ignore state_referenced_locally
  let loading = $state(initialLoading);
  // svelte-ignore state_referenced_locally
  let loadError = $state<ApiClientError | Error | null>(initialError);
  // svelte-ignore state_referenced_locally
  let status = $state<TOTPStatus | null>(initialStatus);

  // begin 之后本次会话内的待确认材料：GET 不返回 secret，刷新即丢失（需要重新开始）。
  // svelte-ignore state_referenced_locally
  let secret = $state(initialSecret);
  // svelte-ignore state_referenced_locally
  let otpauthUrl = $state(initialOtpauthUrl);
  // confirm / recovery 一次性返回的恢复码明文，只在内存里保留到下一次操作。
  // svelte-ignore state_referenced_locally
  let recoveryCodes = $state<string[]>(initialRecoveryCodes);

  let confirmCode = $state('');
  let disablePassword = $state('');
  let recoveryPassword = $state('');

  let beginning = $state(false);
  let confirming = $state(false);
  let disabling = $state(false);
  let regenerating = $state(false);

  let actionError = $state('');
  let notice = $state('');

  /** 读取状态；未登录由服务端 401 决定，前端不猜测。 */
  async function load(): Promise<void> {
    loading = true;
    loadError = null;
    try {
      status = await apiClient.getTOTPStatus();
    } catch (err) {
      loadError = err instanceof Error ? err : new Error(String(err));
    } finally {
      loading = false;
    }
  }

  /** 稳定错误 code → 本地化 key；未知 code 回落到通用失败提示。 */
  function actionErrorKey(err: unknown): string {
    const code = err instanceof ApiClientError ? err.code : '';
    const map: Record<string, string> = {
      invalid_code: 'settings.totp.error.invalid_code',
      already_enabled: 'settings.totp.error.already_enabled',
      no_pending_setup: 'settings.totp.error.no_pending',
      not_enabled: 'settings.totp.error.not_enabled',
      invalid_password: 'settings.totp.error.password_wrong',
      invalid_request: 'settings.totp.error.invalid_request',
    };
    return map[code] || 'settings.totp.error.failed';
  }

  function loadErrorKey(): string {
    if (loadError instanceof ApiClientError && loadError.isUnauthorized) return 'error.unauthorized';
    return 'settings.totp.failed';
  }

  function resetMessages(): void {
    actionError = '';
    notice = '';
  }

  /** 开始绑定：响应里一次性拿到 secret 与 otpauth 链接。 */
  async function begin(): Promise<void> {
    resetMessages();
    beginning = true;
    try {
      const res = await apiClient.beginTOTP();
      secret = res.secret;
      otpauthUrl = res.otpauth_url;
      recoveryCodes = [];
    } catch (err) {
      actionError = actionErrorKey(err);
    } finally {
      beginning = false;
    }
  }

  /** 确认绑定：成功后启用并一次性展示恢复码。 */
  async function confirm(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    resetMessages();
    confirming = true;
    try {
      const res = await apiClient.confirmTOTP(confirmCode.trim());
      status = { enabled: true, pending: false, recovery_remaining: res.recovery_remaining };
      recoveryCodes = res.recovery_codes;
      secret = '';
      otpauthUrl = '';
      confirmCode = '';
      notice = 'settings.totp.saved.enabled';
    } catch (err) {
      actionError = actionErrorKey(err);
    } finally {
      confirming = false;
    }
  }

  /** 关闭 TOTP：需要密码确认。 */
  async function disable(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    resetMessages();
    disabling = true;
    try {
      await apiClient.disableTOTP(disablePassword);
      status = { enabled: false, pending: false, recovery_remaining: 0 };
      recoveryCodes = [];
      disablePassword = '';
      notice = 'settings.totp.saved.disabled';
    } catch (err) {
      actionError = actionErrorKey(err);
    } finally {
      disabling = false;
    }
  }

  /** 重新生成恢复码：需要密码确认，旧码立即作废。 */
  async function regenerate(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    resetMessages();
    regenerating = true;
    try {
      const res = await apiClient.regenerateTOTPRecovery(recoveryPassword);
      recoveryCodes = res.recovery_codes;
      if (status) {
        status = { ...status, recovery_remaining: res.recovery_remaining };
      }
      recoveryPassword = '';
      notice = 'settings.totp.saved.recovery';
    } catch (err) {
      actionError = actionErrorKey(err);
    } finally {
      regenerating = false;
    }
  }

  onMount(() => {
    if (initialStatus === null && initialError === null) {
      load();
    }
  });
</script>

<div class="mx-auto max-w-3xl space-y-6 px-4 py-10" data-testid="totp-view">
  <a
    href="/settings"
    data-testid="totp-back"
    class="inline-block text-sm font-medium text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors"
  >
    &larr; {$t('nav.settings')}
  </a>

  {#if loading}
    <div data-testid="totp-loading" class="py-12 text-center text-zinc-500 dark:text-zinc-400">
      <div class="inline-block animate-spin w-6 h-6 border-2 border-current border-t-transparent rounded-full mb-3" aria-hidden="true"></div>
      <p class="text-sm">{$t('common.loading')}</p>
    </div>
  {:else if loadError}
    <div data-testid="totp-failed" class="card-elevated p-8 rounded-xl text-center">
      <p role="alert" class="text-base font-medium text-zinc-900 dark:text-zinc-100">{$t(loadErrorKey())}</p>
      <button
        type="button"
        data-testid="totp-retry"
        class="mt-4 px-4 py-2 text-sm font-medium rounded-lg bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900 hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors cursor-pointer"
        onclick={() => load()}
      >
        {$t('common.retry')}
      </button>
    </div>
  {:else if status}
    <header class="space-y-1">
      <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100" data-testid="totp-title">
        {$t('settings.totp.heading')}
      </h1>
      <p class="text-sm text-zinc-600 dark:text-zinc-400 leading-relaxed">{$t('settings.totp.intro')}</p>
      <p data-testid="totp-status" class="inline-flex items-center rounded-full bg-zinc-100 dark:bg-zinc-800 px-3 py-1 text-xs font-semibold text-zinc-600 dark:text-zinc-400">
        {status.enabled ? $t('settings.totp.status.enabled') : $t('settings.totp.status.disabled')}
      </p>
    </header>

    {#if actionError}
      <div data-testid="totp-action-error" role="alert" class="rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300">
        {$t(actionError)}
      </div>
    {/if}
    {#if notice}
      <div data-testid="totp-notice" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
        {$t(notice)}
      </div>
    {/if}

    {#if !status.enabled}
      <section class="card-elevated p-6 rounded-xl space-y-4" data-testid="totp-begin-section">
        <p class="text-sm text-zinc-600 dark:text-zinc-400 leading-relaxed">{$t('settings.totp.begin.hint')}</p>
        {#if status.pending && !secret}
          <p data-testid="totp-pending-hint" class="text-xs text-amber-700 dark:text-amber-400">{$t('settings.totp.begin.restart_hint')}</p>
        {/if}
        <button
          type="button"
          data-testid="totp-begin"
          disabled={beginning}
          onclick={() => begin()}
          class="inline-flex items-center justify-center rounded-xl bg-zinc-950 px-5 py-2.5 text-sm font-semibold text-white shadow-xs hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-white active:scale-[0.98] transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {$t(beginning ? 'common.loading' : 'settings.totp.begin.submit')}
        </button>
      </section>
    {/if}

    {#if secret}
      <section class="card-elevated p-6 rounded-xl space-y-4" data-testid="totp-pending">
        <h2 class="text-lg font-bold tracking-tight text-zinc-900 dark:text-zinc-100">{$t('settings.totp.pending.heading')}</h2>
        <div class="rounded-xl border border-zinc-100 dark:border-zinc-800 bg-zinc-50/70 dark:bg-zinc-800/50 p-4 space-y-3">
          <div>
            <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('settings.totp.pending.secret_label')}</span>
            <p data-testid="totp-secret" class="mt-1 font-mono text-sm font-semibold text-zinc-900 dark:text-zinc-100 break-all select-all">{secret}</p>
          </div>
          <div>
            <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('settings.totp.pending.otpauth_label')}</span>
            <p data-testid="totp-otpauth" class="mt-1 font-mono text-xs text-zinc-600 dark:text-zinc-400 break-all select-all">{otpauthUrl}</p>
          </div>
        </div>
        <form onsubmit={confirm} class="space-y-4" data-testid="totp-confirm-form">
          <p class="text-sm text-zinc-600 dark:text-zinc-400">{$t('settings.totp.pending.confirm_hint')}</p>
          <label class="block">
            <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('settings.totp.pending.code_label')}</span>
            <input
              data-testid="totp-confirm-code"
              type="text"
              inputmode="numeric"
              autocomplete="one-time-code"
              bind:value={confirmCode}
              required
              class="mt-1.5 w-full rounded-xl border border-zinc-200 bg-white px-3.5 py-2.5 text-sm font-mono text-zinc-900 shadow-2xs focus:border-zinc-900 focus:outline-none focus:ring-1 focus:ring-zinc-900 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:focus:border-zinc-100 dark:focus:ring-zinc-100 transition-colors"
            />
          </label>
          <button
            type="submit"
            data-testid="totp-confirm-submit"
            disabled={confirming}
            class="inline-flex items-center justify-center rounded-xl bg-zinc-950 px-5 py-2.5 text-sm font-semibold text-white shadow-xs hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-white active:scale-[0.98] transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {$t(confirming ? 'common.loading' : 'settings.totp.pending.confirm_submit')}
          </button>
        </form>
      </section>
    {/if}

    {#if status.enabled}
      <section class="card-elevated p-6 rounded-xl space-y-4" data-testid="totp-disable">
        <div>
          <h2 class="text-lg font-bold tracking-tight text-zinc-900 dark:text-zinc-100">{$t('settings.totp.disable.heading')}</h2>
          <p class="mt-1 text-xs text-zinc-500 dark:text-zinc-400">{$t('settings.totp.disable.hint')}</p>
        </div>
        <form onsubmit={disable} class="space-y-4">
          <label class="block">
            <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('settings.totp.disable.password_label')}</span>
            <input
              data-testid="totp-disable-password"
              type="password"
              autocomplete="current-password"
              bind:value={disablePassword}
              required
              class="mt-1.5 w-full rounded-xl border border-zinc-200 bg-white px-3.5 py-2.5 text-sm text-zinc-900 shadow-2xs focus:border-zinc-900 focus:outline-none focus:ring-1 focus:ring-zinc-900 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:focus:border-zinc-100 dark:focus:ring-zinc-100 transition-colors"
            />
          </label>
          <button
            type="submit"
            data-testid="totp-disable-submit"
            disabled={disabling}
            class="inline-flex items-center justify-center rounded-xl border border-rose-200 bg-white px-4 py-2 text-sm font-semibold text-rose-700 shadow-2xs hover:bg-rose-50 hover:border-rose-300 dark:border-rose-900/60 dark:bg-rose-950/40 dark:text-rose-300 dark:hover:bg-rose-900/60 active:scale-[0.98] transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {$t(disabling ? 'common.loading' : 'settings.totp.disable.submit')}
          </button>
        </form>
      </section>

      <section class="card-elevated p-6 rounded-xl space-y-4" data-testid="totp-recovery">
        <div>
          <h2 class="text-lg font-bold tracking-tight text-zinc-900 dark:text-zinc-100">{$t('settings.totp.recovery.heading')}</h2>
          <p data-testid="totp-recovery-remaining" class="mt-1 text-xs text-zinc-500 dark:text-zinc-400">
            {$t('settings.totp.recovery.remaining', { count: status.recovery_remaining })}
          </p>
        </div>
        <div class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-xs text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300" role="alert">
          {$t('settings.totp.recovery.warning')}
        </div>
        {#if recoveryCodes.length > 0}
          <div class="space-y-2">
            <p class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('settings.totp.recovery.list_label')}</p>
            <ul data-testid="totp-recovery-codes" class="grid grid-cols-2 gap-2 font-mono text-xs">
              {#each recoveryCodes as code}
                <li class="rounded-lg border border-zinc-100 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-800 p-2 text-center text-zinc-800 dark:text-zinc-200 select-all">{code}</li>
              {/each}
            </ul>
          </div>
        {/if}
        <form onsubmit={regenerate} class="space-y-4 pt-2">
          <label class="block">
            <span class="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{$t('settings.totp.recovery.password_label')}</span>
            <input
              data-testid="totp-recovery-password"
              type="password"
              autocomplete="current-password"
              bind:value={recoveryPassword}
              required
              class="mt-1.5 w-full rounded-xl border border-zinc-200 bg-white px-3.5 py-2.5 text-sm text-zinc-900 shadow-2xs focus:border-zinc-900 focus:outline-none focus:ring-1 focus:ring-zinc-900 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:focus:border-zinc-100 dark:focus:ring-zinc-100 transition-colors"
            />
          </label>
          <button
            type="submit"
            data-testid="totp-recovery-submit"
            disabled={regenerating}
            class="inline-flex items-center justify-center rounded-xl border border-zinc-200 bg-white px-4 py-2 text-sm font-semibold text-zinc-700 shadow-2xs hover:bg-zinc-50 hover:border-zinc-300 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-700 active:scale-[0.98] transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {$t(regenerating ? 'common.loading' : 'settings.totp.recovery.regenerate_submit')}
          </button>
        </form>
      </section>
    {/if}
  {/if}
</div>
