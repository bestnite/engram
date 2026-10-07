<script lang="ts">
  import { t } from '../i18n';
  import { navigate } from '../router';
  import { login, authStore } from '../auth';
  import { getApiErrorMessageKey, ApiClientError } from '../api';
  import OIDCLoginEntry from '../components/OIDCLoginEntry.svelte';
  import Button from '../components/ui/Button.svelte';

  let username = $state('');
  let password = $state('');
  let loading = $state(false);
  let errorKey = $state<string | null>(null);

  // 已登录用户访问登录页时直接送去控制台：这是体验问题，不是安全兜底——
  // CSRF 层已按「本次请求有没有有效会话」分档，已登录时用会话绑定 token 校验，不再 403。
  // 用 $effect 而不是 onMount：整页加载时 initAuth() 是异步的，onMount 时认证状态还没就绪。
  $effect(() => {
    if ($authStore.initialized && $authStore.authenticated) {
      navigate('/');
    }
  });

  async function handleSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!username.trim() || !password) {
      return;
    }
    loading = true;
    errorKey = null;

    try {
      const res = await login(username.trim(), password);
      if (res.requires_totp) {
        // 第一因素通过、需要第二因素：进入 SPA 第二步（协议 GET/POST /api/v1/auth/totp）。
        // 第二步凭据是服务端在本次响应里下发的 HttpOnly cookie，因此必须立刻导航过去。
        navigate('/login/totp');
      } else if (res.authenticated) {
        navigate('/');
      }
    } catch (err) {
      if (err instanceof ApiClientError) {
        errorKey = getApiErrorMessageKey(err);
      } else {
        errorKey = 'error.unknown';
      }
    } finally {
      loading = false;
    }
  }
</script>

<div class="py-12 max-w-md mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl">
    <div class="mb-6 text-center">
      <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
        {$t('auth.login.heading')}
      </h1>
    </div>

    {#if errorKey}
      <div
        data-testid="login-error"
        class="mb-6 p-4 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-800/60 text-rose-700 dark:text-rose-300 text-sm flex items-center space-x-2"
      >
        <svg class="w-5 h-5 flex-shrink-0 text-rose-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <span>{$t(errorKey)}</span>
      </div>
    {/if}

    <form onsubmit={handleSubmit} class="space-y-4">
      <div>
        <label for="login-username" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
          {$t('auth.field.username')}
        </label>
        <input
          id="login-username"
          name="username"
          type="text"
          autocomplete="username"
          required
          bind:value={username}
          disabled={loading}
          class="field-input text-sm w-full transition-colors disabled:opacity-50"
        />
      </div>

      <div>
        <label for="login-password" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
          {$t('auth.field.password')}
        </label>
        <input
          id="login-password"
          name="password"
          type="password"
          autocomplete="current-password"
          required
          bind:value={password}
          disabled={loading}
          class="field-input text-sm w-full transition-colors disabled:opacity-50"
        />
      </div>

      <div class="pt-2">
        <Button type="submit" disabled={loading || !username.trim() || !password} variant="primary" size="lg" class="w-full">
          {#if loading}
            <div class="w-4 h-4 border-2 border-current border-t-transparent rounded-full animate-spin" aria-hidden="true"></div>
            <span>{$t('auth.login.submitting')}</span>
          {:else}
            <span>{$t('auth.login.submit')}</span>
          {/if}
        </Button>
      </div>
    </form>

    <!-- OIDC 可选登录：组件挂载时探测服务端配置，仅在 enabled 时渲染入口 -->
    <OIDCLoginEntry />
  </div>
</div>
