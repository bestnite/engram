<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import { getAccountErrorMessageKey } from '../api/account-errors';

  // 改邮箱确认结果的 SPA 视图（服务端 GET /confirm-email-change 返回应用壳，服务端不再渲染页面）。
  // 令牌由本视图读取 ?token= 后经 POST /api/v1/auth/confirm-email-change 消费（一次性、有过期）。
    const token =
    typeof window !== 'undefined'
      ? (new URLSearchParams(window.location.search).get('token') ?? '')
      : '';

  let status = $state<'checking' | 'done' | 'error'>('checking');
  let errorKey = $state<string | null>(null);

  onMount(async () => {
    if (!token) {
      status = 'error';
      errorKey = 'account.error.token_invalid';
      return;
    }
    try {
      await apiClient.confirmEmailChange(token);
      status = 'done';
    } catch (err) {
      status = 'error';
      errorKey = getAccountErrorMessageKey(err);
    }
  });
</script>

<div class="py-12 max-w-md mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl text-center">
    <h1 class="mb-4 text-2xl font-bold tracking-tight text-foreground">
      {$t('account.confirm_change.heading')}
    </h1>

    {#if status === 'checking'}
      <p data-testid="confirm-change-checking" class="text-sm text-muted-foreground">{$t('account.confirm_change.checking')}</p>
    {:else if status === 'done'}
      <p data-testid="confirm-change-done" class="text-sm text-emerald-600 dark:text-emerald-400">{$t('account.confirm_change.done')}</p>
    {:else}
      <p data-testid="confirm-change-error" class="text-sm text-rose-600 dark:text-rose-400">{errorKey ? $t(errorKey) : $t('account.confirm_change.failed')}</p>
    {/if}

    <div class="mt-6 text-sm">
      <a href="/login" class="text-muted-foreground hover:text-foreground transition-colors">
        {$t('account.confirm_change.back_login')}
      </a>
    </div>
  </div>
</div>
