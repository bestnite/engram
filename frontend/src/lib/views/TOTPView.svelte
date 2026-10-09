<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import SettingsSection from '../components/ui/SettingsSection.svelte';
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient, ApiClientError } from '../api';
  import type { TOTPStatus } from '../api';
  import Skeleton from '../components/ui/Skeleton.svelte';
  import Button from '../components/ui/Button.svelte';
  import Badge from '../components/ui/Badge.svelte';

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

<Page testId="totp-view">
  <PageHeader
    title={$t('settings.totp.heading')}
    testId="totp-title"
    description={$t('settings.totp.intro')}
    back={{ href: '/settings', label: $t('nav.settings'), testId: 'totp-back' }}
  >
    {#snippet meta()}
      {#if status}
        <Badge testId="totp-status" variant={status.enabled ? 'success' : 'neutral'}>
          {status.enabled ? $t('settings.totp.status.enabled') : $t('settings.totp.status.disabled')}
        </Badge>
      {/if}
    {/snippet}
  </PageHeader>

  {#if loading}
    <Skeleton testId="totp-loading" label={$t('common.loading')} />
  {:else if loadError}
    <div data-testid="totp-failed" class="py-16 text-center">
      <p role="alert" class="text-base font-medium text-foreground">{$t(loadErrorKey())}</p>
      <Button type="button" testId="totp-retry" onclick={() => load()} variant="outline" size="lg" class="mt-4">
        {$t('common.retry')}
      </Button>
    </div>
  {:else if status}
    {#if actionError}
      <p data-testid="totp-action-error" role="alert" class="mb-4 text-sm text-destructive-foreground">{$t(actionError)}</p>
    {/if}
    {#if notice}
      <p data-testid="totp-notice" role="status" class="mb-4 text-sm text-success">{$t(notice)}</p>
    {/if}

    {#if !status.enabled}
      <SettingsSection title={$t('settings.totp.begin.heading')} description={$t('settings.totp.begin.hint')} testId="totp-begin-section">
        {#if status.pending && !secret}
          <p data-testid="totp-pending-hint" class="mb-3 text-[13px] text-warning">{$t('settings.totp.begin.restart_hint')}</p>
        {/if}
        <Button type="button" testId="totp-begin" disabled={beginning} onclick={() => begin()} variant={secret ? 'outline' : 'primary'} size="lg">
          {$t(beginning ? 'common.loading' : 'settings.totp.begin.submit')}
        </Button>
      </SettingsSection>
    {/if}

    {#if secret}
      <SettingsSection title={$t('settings.totp.pending.heading')} description={$t('settings.totp.pending.confirm_hint')} testId="totp-pending">
        <dl class="max-w-xl space-y-3 rounded-lg bg-surface p-4">
          <div>
            <dt class="text-xs text-muted-foreground">{$t('settings.totp.pending.secret_label')}</dt>
            <dd data-testid="totp-secret" class="mt-1 select-all break-all font-mono text-sm font-semibold text-foreground">{secret}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted-foreground">{$t('settings.totp.pending.otpauth_label')}</dt>
            <dd data-testid="totp-otpauth" class="mt-1 select-all break-all font-mono text-xs text-muted-foreground">{otpauthUrl}</dd>
          </div>
        </dl>
        <form onsubmit={confirm} class="mt-5 flex flex-wrap items-end gap-2" data-testid="totp-confirm-form">
          <label class="block text-sm font-medium text-foreground">{$t('settings.totp.pending.code_label')}
            <input
              data-testid="totp-confirm-code"
              type="text"
              inputmode="numeric"
              autocomplete="one-time-code"
              bind:value={confirmCode}
              required
              class="field-input mt-1.5 block w-40 font-mono text-sm font-normal tracking-widest"
            />
          </label>
          <Button type="submit" testId="totp-confirm-submit" disabled={confirming} variant="primary" size="lg">
            {$t(confirming ? 'common.loading' : 'settings.totp.pending.confirm_submit')}
          </Button>
        </form>
      </SettingsSection>
    {/if}

    {#if status.enabled}
      <SettingsSection title={$t('settings.totp.recovery.heading')} description={$t('settings.totp.recovery.warning')} testId="totp-recovery">
        <p data-testid="totp-recovery-remaining" class="text-sm text-foreground">
          {$t('settings.totp.recovery.remaining', { count: status.recovery_remaining })}
        </p>
        {#if recoveryCodes.length > 0}
          <div class="mt-4 max-w-xl">
            <p class="text-xs text-muted-foreground">{$t('settings.totp.recovery.list_label')}</p>
            <ul data-testid="totp-recovery-codes" class="mt-1.5 grid grid-cols-2 gap-x-6 gap-y-1 rounded-lg bg-surface p-4 font-mono text-sm sm:grid-cols-3">
              {#each recoveryCodes as code}
                <li class="select-all text-foreground">{code}</li>
              {/each}
            </ul>
          </div>
        {/if}
        <form onsubmit={regenerate} class="mt-5 flex flex-wrap items-end gap-2">
          <label class="block text-sm font-medium text-foreground">{$t('settings.totp.recovery.password_label')}
            <input
              data-testid="totp-recovery-password"
              type="password"
              autocomplete="current-password"
              bind:value={recoveryPassword}
              required
              class="field-input mt-1.5 block w-64 text-sm font-normal"
            />
          </label>
          <Button type="submit" testId="totp-recovery-submit" disabled={regenerating} variant="outline" size="lg">
            {$t(regenerating ? 'common.loading' : 'settings.totp.recovery.regenerate_submit')}
          </Button>
        </form>
      </SettingsSection>

      <!-- 危险操作放在最后。 -->
      <SettingsSection title={$t('settings.totp.disable.heading')} description={$t('settings.totp.disable.hint')} testId="totp-disable">
        <form onsubmit={disable} class="flex flex-wrap items-end gap-2">
          <label class="block text-sm font-medium text-foreground">{$t('settings.totp.disable.password_label')}
            <input
              data-testid="totp-disable-password"
              type="password"
              autocomplete="current-password"
              bind:value={disablePassword}
              required
              class="field-input mt-1.5 block w-64 text-sm font-normal"
            />
          </label>
          <Button type="submit" testId="totp-disable-submit" disabled={disabling} variant="danger-outline" size="lg">
            {$t(disabling ? 'common.loading' : 'settings.totp.disable.submit')}
          </Button>
        </form>
      </SettingsSection>
    {/if}
  {/if}
</Page>
