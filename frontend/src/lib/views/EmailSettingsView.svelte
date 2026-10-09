<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import { getAccountErrorMessageKey } from '../api/account-errors';
  import Button from '../components/ui/Button.svelte';

  // 账号与邮箱设置（服务端 GET /settings/email 切壳后由客户端路由渲染此页，未登录在服务端即重定向）。
  // 读取与提交走 /api/v1/settings/email，重发验证走 /api/v1/settings/verify-email。
  let email = $state('');
  let verified = $state(false);
  let mailReady = $state(true);
  let loaded = $state(false);
  let loadErrorKey = $state<string | null>(null);

  let newEmail = $state('');
  let submitting = $state(false);
  let errorKey = $state<string | null>(null);
  let sent = $state(false);

  let resending = $state(false);
  let resendDone = $state(false);
  let resendErrorKey = $state<string | null>(null);

  onMount(async () => {
    try {
      const res = await apiClient.getEmailSettings();
      email = res.email;
      verified = res.email_verified;
      mailReady = res.mail_ready;
    } catch (err) {
      loadErrorKey = getAccountErrorMessageKey(err);
    } finally {
      loaded = true;
    }
  });

  async function handleSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    submitting = true;
    errorKey = null;
    sent = false;
    try {
      await apiClient.requestEmailChange({ email: newEmail.trim() });
      sent = true;
    } catch (err) {
      errorKey = getAccountErrorMessageKey(err);
    } finally {
      submitting = false;
    }
  }

  async function handleResend(): Promise<void> {
    resending = true;
    resendErrorKey = null;
    resendDone = false;
    try {
      await apiClient.resendVerification();
      resendDone = true;
    } catch (err) {
      resendErrorKey = getAccountErrorMessageKey(err);
    } finally {
      resending = false;
    }
  }
</script>

<div class="py-12 max-w-lg mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl">
    <div class="mb-6">
      <h1 class="text-2xl font-bold tracking-tight text-foreground">
        {$t('account.email.heading')}
      </h1>
      <p class="mt-2 text-sm text-muted-foreground">{$t('account.email.intro')}</p>
    </div>

    {#if !loaded}
      <p class="text-sm text-muted-foreground">{$t('account.email.loading')}</p>
    {:else if loadErrorKey}
      <div
        data-testid="email-load-error"
        class="p-4 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-800/60 text-rose-700 dark:text-rose-300 text-sm"
      >
        <span>{$t(loadErrorKey)}</span>
      </div>
    {:else}
      <dl class="mb-6 space-y-2 text-sm">
        <div class="flex items-center justify-between gap-4">
          <dt class="text-muted-foreground">{$t('account.email.current_label')}</dt>
          <dd data-testid="email-current" class="font-medium text-foreground break-all">{email}</dd>
        </div>
        <div class="flex items-center justify-between gap-4">
          <dt class="text-muted-foreground">{$t('account.email.status_label')}</dt>
          <dd data-testid="email-status" class="font-medium {verified ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'}">
            {verified ? $t('account.email.verified') : $t('account.email.unverified')}
          </dd>
        </div>
      </dl>

      {#if !verified && mailReady}
        <div class="mb-6">
          <button
            type="button"
            onclick={handleResend}
            disabled={resending}
            class="px-4 py-2 rounded-lg border border-input text-sm text-foreground/80 hover:bg-muted transition-colors disabled:opacity-50 cursor-pointer"
          >
            {resending ? $t('account.email.resending') : $t('account.email.resend')}
          </button>
          {#if resendDone}
            <p data-testid="resend-done" class="mt-2 text-sm text-emerald-600 dark:text-emerald-400">{$t('account.email.resent')}</p>
          {/if}
          {#if resendErrorKey}
            <p data-testid="resend-error" class="mt-2 text-sm text-rose-600 dark:text-rose-400">{$t(resendErrorKey)}</p>
          {/if}
        </div>
      {/if}

      <hr class="my-6 border-border" />

      <h2 class="mb-4 text-lg font-semibold text-foreground">{$t('account.email.change_heading')}</h2>

      {#if sent}
        <div
          data-testid="email-change-sent"
          class="mb-4 p-4 rounded-lg bg-muted border border-input text-foreground/80 text-sm"
        >
          <span>{$t('account.email.change_sent')}</span>
        </div>
      {/if}

      {#if errorKey}
        <div
          data-testid="email-change-error"
          class="mb-4 p-4 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-800/60 text-rose-700 dark:text-rose-300 text-sm"
        >
          <span>{$t(errorKey)}</span>
        </div>
      {/if}

      {#if !mailReady}
        <div
          data-testid="email-not-configured"
          class="mb-4 p-4 rounded-lg bg-amber-50 dark:bg-amber-950/40 border border-amber-200 dark:border-amber-800/60 text-amber-700 dark:text-amber-300 text-sm"
        >
          <span>{$t('account.error.mail_not_configured')}</span>
        </div>
      {/if}

      <form onsubmit={handleSubmit} class="space-y-4">
        <div>
          <label for="email-new" class="block text-sm font-medium text-foreground/80 mb-1.5">
            {$t('account.email.change_label')}
          </label>
          <input
            id="email-new"
            name="email"
            type="email"
            autocomplete="email"
            required
            bind:value={newEmail}
            disabled={submitting || !mailReady}
            class="field-input text-sm w-full transition-colors disabled:opacity-50"
          />
        </div>

        <div class="pt-2">
          <Button type="submit" disabled={submitting || !mailReady || !newEmail.trim()} variant="primary" size="lg" class="w-full">
            {#if submitting}
              <span>{$t('account.email.change_submitting')}</span>
            {:else}
              <span>{$t('account.email.change_submit')}</span>
            {/if}
          </Button>
        </div>
      </form>

      <div class="mt-6 text-center text-sm">
        <a href="/settings" class="text-muted-foreground hover:text-foreground transition-colors">
          {$t('account.email.back_settings')}
        </a>
      </div>
    {/if}
  </div>
</div>
