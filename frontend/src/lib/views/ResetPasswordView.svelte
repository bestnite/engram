<script lang="ts">
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import { getAccountErrorMessageKey } from '../api/account-errors';
  import Button from '../components/ui/Button.svelte';

  // 设置新密码（服务端 GET /reset-password 切壳后由客户端路由渲染此页）。
  // token 由邮件的重置链接带入查询串，提交走 POST /api/v1/auth/reset-password。
  const token =
    typeof window !== 'undefined'
      ? (new URLSearchParams(window.location.search).get('token') ?? '')
      : '';

  let password = $state('');
  let loading = $state(false);
  let errorKey = $state<string | null>(null);
  let done = $state(false);

  async function handleSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    loading = true;
    errorKey = null;
    try {
      await apiClient.resetPassword({ token, password });
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
        {$t('account.reset.heading')}
      </h1>
    </div>

    {#if done}
      <div
        data-testid="reset-done"
        class="mb-6 p-4 rounded-lg bg-zinc-100 dark:bg-zinc-800/60 border border-zinc-200 dark:border-zinc-700 text-zinc-700 dark:text-zinc-300 text-sm"
      >
        <span>{$t('account.reset.done')}</span>
      </div>
      <a
        href="/spa/login"
        class="block text-center text-sm text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors"
      >
        {$t('account.reset.back_login')}
      </a>
    {:else if !token}
      <div
        data-testid="reset-invalid"
        class="mb-6 p-4 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-800/60 text-rose-700 dark:text-rose-300 text-sm"
      >
        <span>{$t('account.error.token_invalid')}</span>
      </div>
      <a
        href="/spa/login"
        class="block text-center text-sm text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors"
      >
        {$t('account.reset.back_login')}
      </a>
    {:else}
      <p class="mb-6 text-sm text-zinc-600 dark:text-zinc-400">{$t('account.reset.intro')}</p>

      {#if errorKey}
        <div
          data-testid="reset-error"
          class="mb-6 p-4 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-800/60 text-rose-700 dark:text-rose-300 text-sm"
        >
          <span>{$t(errorKey)}</span>
        </div>
      {/if}

      <form onsubmit={handleSubmit} class="space-y-4">
        <div>
          <label for="reset-password" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
            {$t('account.reset.new_password_label')}
          </label>
          <input
            id="reset-password"
            name="password"
            type="password"
            autocomplete="new-password"
            required
            bind:value={password}
            disabled={loading}
            class="field-input text-sm w-full transition-colors disabled:opacity-50"
          />
        </div>

        <div class="pt-2">
          <Button type="submit" disabled={loading || !password} variant="primary" size="lg" class="w-full">
            {#if loading}
              <span>{$t('account.reset.submitting')}</span>
            {:else}
              <span>{$t('account.reset.submit')}</span>
            {/if}
          </Button>
        </div>
      </form>
    {/if}
  </div>
</div>
