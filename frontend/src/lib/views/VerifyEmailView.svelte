<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import { getAccountErrorMessageKey } from '../api/account-errors';

  // 邮箱验证结果的 SPA 视图（服务端 GET /verify-email 返回应用壳，服务端不再渲染页面）。
  // 令牌由本视图读取 ?token= 后经 POST /api/v1/auth/verify-email 消费（一次性、有过期）。
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
      await apiClient.verifyEmail(token);
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
      {$t('account.verify.heading')}
    </h1>

    {#if status === 'checking'}
      <p data-testid="verify-checking" class="text-sm text-muted-foreground">{$t('account.verify.checking')}</p>
    {:else if status === 'done'}
      <p data-testid="verify-done" class="text-sm text-emerald-600 dark:text-emerald-400">{$t('account.verify.done')}</p>
    {:else}
      <p data-testid="verify-error" class="text-sm text-rose-600 dark:text-rose-400">{errorKey ? $t(errorKey) : $t('account.verify.failed')}</p>
    {/if}

    <div class="mt-6 text-sm">
      <a href="/login" class="text-muted-foreground hover:text-foreground transition-colors">
        {$t('account.verify.back_login')}
      </a>
    </div>
  </div>
</div>
