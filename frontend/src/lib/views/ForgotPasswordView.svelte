<script lang="ts">
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import { getAccountErrorMessageKey } from '../api/account-errors';
  import Button from '../components/ui/Button.svelte';

  // 请求密码重置（服务端 GET /forgot-password 切壳后由客户端路由渲染此页）。
  // 协议走 POST /api/v1/auth/forgot-password：无论账号是否存在都回同形响应，避免账号枚举。
  let email = $state('');
  let loading = $state(false);
  let errorKey = $state<string | null>(null);
  let done = $state(false);
  let mailReady = $state(true);

  async function handleSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    loading = true;
    errorKey = null;
    try {
      const res = await apiClient.requestPasswordReset({ email: email.trim() });
      mailReady = res.mail_ready;
      done = true;
    } catch (err) {
      errorKey = getAccountErrorMessageKey(err);
    } finally {
      loading = false;
    }
  }
</script>

<div class="py-12 max-w-md mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl">
    <div class="mb-6 text-center">
      <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
        {$t('account.forgot.heading')}
      </h1>
    </div>

    {#if done}
      <div
        data-testid="forgot-done"
        class="mb-6 p-4 rounded-lg bg-zinc-100 dark:bg-zinc-800/60 border border-zinc-200 dark:border-zinc-700 text-zinc-700 dark:text-zinc-300 text-sm"
      >
        <span>{mailReady ? $t('account.forgot.sent') : $t('account.error.mail_not_configured')}</span>
      </div>
      <a
        href="/spa/login"
        class="block text-center text-sm text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors"
      >
        {$t('account.forgot.back_login')}
      </a>
    {:else}
      <p class="mb-6 text-sm text-zinc-600 dark:text-zinc-400">{$t('account.forgot.intro')}</p>

      {#if errorKey}
        <div
          data-testid="forgot-error"
          class="mb-6 p-4 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-800/60 text-rose-700 dark:text-rose-300 text-sm"
        >
          <span>{$t(errorKey)}</span>
        </div>
      {/if}

      <form onsubmit={handleSubmit} class="space-y-4">
        <div>
          <label for="forgot-email" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
            {$t('account.forgot.email_label')}
          </label>
          <input
            id="forgot-email"
            data-testid="forgot-email"
            name="email"
            type="email"
            autocomplete="email"
            required
            bind:value={email}
            disabled={loading}
            class="field-input text-sm w-full transition-colors disabled:opacity-50"
          />
        </div>

        <div class="pt-2">
          <Button type="submit" disabled={loading || !email.trim()} variant="primary" size="lg" class="w-full" testId="forgot-submit">
            {#if loading}
              <span>{$t('account.forgot.submitting')}</span>
            {:else}
              <span>{$t('account.forgot.submit')}</span>
            {/if}
          </Button>
        </div>
      </form>

      <div class="mt-6 text-center text-sm">
        <a href="/spa/login" class="text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors">
          {$t('account.forgot.back_login')}
        </a>
      </div>
    {/if}
  </div>
</div>
