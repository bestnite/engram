<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import SettingsSection from '../components/ui/SettingsSection.svelte';
  import Badge from '../components/ui/Badge.svelte';
  import Skeleton from '../components/ui/Skeleton.svelte';
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

<Page>
  <PageHeader
    title={$t('account.email.heading')}
    description={$t('account.email.intro')}
    back={{ href: '/settings', label: $t('account.email.back_settings') }}
  />

  {#if !loaded}
    <Skeleton testId="email-loading" label={$t('account.email.loading')} lines={2} />
  {:else if loadErrorKey}
    <p data-testid="email-load-error" role="alert" class="py-16 text-center text-sm text-destructive-foreground">{$t(loadErrorKey)}</p>
  {:else}
    <SettingsSection title={$t('account.email.current_label')}>
      <div class="flex flex-wrap items-center gap-3">
        <span data-testid="email-current" class="break-all text-sm font-medium text-foreground">{email}</span>
        <Badge variant={verified ? 'success' : 'warning'}><span data-testid="email-status">{verified ? $t('account.email.verified') : $t('account.email.unverified')}</span></Badge>
      </div>
      {#if !verified && mailReady}
        <div class="mt-4 flex flex-wrap items-center gap-3">
          <Button variant="outline" size="lg" onclick={handleResend} disabled={resending}>
            {resending ? $t('account.email.resending') : $t('account.email.resend')}
          </Button>
          {#if resendDone}
            <p data-testid="resend-done" class="text-sm text-success">{$t('account.email.resent')}</p>
          {/if}
          {#if resendErrorKey}
            <p data-testid="resend-error" class="text-sm text-destructive-foreground">{$t(resendErrorKey)}</p>
          {/if}
        </div>
      {/if}
    </SettingsSection>

    <SettingsSection title={$t('account.email.change_heading')} description={$t('account.email.change_hint')}>
      {#if !mailReady}
        <p data-testid="email-not-configured" class="mb-4 text-[13px] text-warning">{$t('account.error.mail_not_configured')}</p>
      {/if}
      <form onsubmit={handleSubmit} class="flex max-w-xl flex-wrap items-end gap-2">
        <div class="min-w-56 flex-1">
          <label for="email-new" class="block text-sm font-medium text-foreground">{$t('account.email.change_label')}</label>
          <input
            id="email-new"
            name="email"
            type="email"
            autocomplete="email"
            required
            bind:value={newEmail}
            disabled={submitting || !mailReady}
            class="field-input mt-1.5 w-full text-sm disabled:opacity-50"
          />
        </div>
        <Button type="submit" disabled={submitting || !mailReady || !newEmail.trim()} variant="primary" size="lg">
          {submitting ? $t('account.email.change_submitting') : $t('account.email.change_submit')}
        </Button>
      </form>
      {#if sent}
        <p data-testid="email-change-sent" class="mt-3 text-sm text-success" role="status">{$t('account.email.change_sent')}</p>
      {/if}
      {#if errorKey}
        <p data-testid="email-change-error" class="mt-3 text-sm text-destructive-foreground" role="alert">{$t(errorKey)}</p>
      {/if}
    </SettingsSection>
  {/if}
</Page>
