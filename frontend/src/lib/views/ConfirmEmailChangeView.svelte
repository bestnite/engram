<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import { getAccountErrorMessageKey } from '../api/account-errors';

  // 改邮箱确认结果的 SPA 视图（挂载于 /spa/confirm-email-change 应用壳）。
  // 免登录的一键链接 /confirm-email-change 仍在服务端消费令牌并渲染结果，无脚本也能完成；此视图
  // 是给 JavaScript 客户端走的独立入口，令牌语义相同（一次性、有过期），协议走
  // POST /api/v1/auth/confirm-email-change。
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
    <h1 class="mb-4 text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
      {$t('account.confirm_change.heading')}
    </h1>

    {#if status === 'checking'}
      <p data-testid="confirm-change-checking" class="text-sm text-zinc-600 dark:text-zinc-400">{$t('account.confirm_change.checking')}</p>
    {:else if status === 'done'}
      <p data-testid="confirm-change-done" class="text-sm text-emerald-600 dark:text-emerald-400">{$t('account.confirm_change.done')}</p>
    {:else}
      <p data-testid="confirm-change-error" class="text-sm text-rose-600 dark:text-rose-400">{errorKey ? $t(errorKey) : $t('account.confirm_change.failed')}</p>
    {/if}

    <div class="mt-6 text-sm">
      <a href="/spa/login" class="text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors">
        {$t('account.confirm_change.back_login')}
      </a>
    </div>
  </div>
</div>
