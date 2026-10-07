<script lang="ts">
  import { t } from '../i18n';
  import { navigate } from '../router';
  import { register, authStore } from '../auth';
  import { getApiErrorMessageKey, ApiClientError } from '../api';

  // 邀请 token 由 URL 查询参数带入（GET /register?invite=... 下发应用壳）。
  const invite =
    typeof window !== 'undefined'
      ? (new URLSearchParams(window.location.search).get('invite') ?? '')
      : '';

  let username = $state('');
  let email = $state('');
  let displayName = $state('');
  let password = $state('');
  let loading = $state(false);
  let errorKey = $state<string | null>(null);

  // 已登录用户访问注册页时直接送去控制台：这是体验问题，不是安全兜底——
  // CSRF 层已按「本次请求有没有有效会话」分档，已登录时用会话绑定 token 校验，不再 403。
  // 用 $effect 而不是 onMount：整页加载时 initAuth() 是异步的，onMount 时认证状态还没就绪。
  $effect(() => {
    if ($authStore.initialized && $authStore.authenticated) {
      navigate('/');
    }
  });


  async function handleSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!username.trim() || !email.trim() || !password) {
      return;
    }
    loading = true;
    errorKey = null;
    try {
      await register({
        username: username.trim(),
        email: email.trim(),
        display_name: displayName.trim(),
        password,
        invite,
      });
      // 注册不建立会话：与 SSR 一致，成功后回到登录页。
      navigate('/login');
    } catch (err) {
      errorKey = err instanceof ApiClientError ? getApiErrorMessageKey(err) : 'error.unknown';
    } finally {
      loading = false;
    }
  }
</script>

<div class="py-12 max-w-md mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl">
    <div class="mb-6 text-center">
      <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
        {$t('auth.register.heading')}
      </h1>
    </div>

    {#if invite}
      <p class="mb-6 text-sm text-zinc-600 dark:text-zinc-400">
        {$t('auth.register.invite_intro')}
      </p>
    {/if}

    {#if errorKey}
      <div
        data-testid="register-error"
        class="mb-6 p-4 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-800/60 text-rose-700 dark:text-rose-300 text-sm"
      >
        <span>{$t(errorKey)}</span>
      </div>
    {/if}

    <form onsubmit={handleSubmit} class="space-y-4">
      <div>
        <label for="register-username" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
          {$t('auth.field.username')}
        </label>
        <input
          id="register-username"
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
        <label for="register-email" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
          {$t('auth.field.email')}
        </label>
        <input
          id="register-email"
          name="email"
          type="email"
          autocomplete="email"
          required
          bind:value={email}
          disabled={loading}
          class="field-input text-sm w-full transition-colors disabled:opacity-50"
        />
      </div>

      <div>
        <label for="register-display-name" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
          {$t('auth.field.display_name')}
        </label>
        <input
          id="register-display-name"
          name="display_name"
          type="text"
          autocomplete="name"
          bind:value={displayName}
          disabled={loading}
          class="field-input text-sm w-full transition-colors disabled:opacity-50"
        />
      </div>

      <div>
        <label for="register-password" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
          {$t('auth.field.password')}
        </label>
        <input
          id="register-password"
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
        <button
          type="submit"
          disabled={loading || !username.trim() || !email.trim() || !password}
          class="w-full py-2.5 px-4 rounded-lg bg-zinc-900 dark:bg-zinc-100 text-white dark:text-zinc-900 font-semibold text-sm hover:bg-zinc-800 dark:hover:bg-zinc-200 transition-colors disabled:opacity-50 btn-press cursor-pointer flex items-center justify-center space-x-2"
        >
          {#if loading}
            <div class="w-4 h-4 border-2 border-current border-t-transparent rounded-full animate-spin" aria-hidden="true"></div>
            <span>{$t('auth.register.submitting')}</span>
          {:else}
            <span>{$t('auth.register.submit')}</span>
          {/if}
        </button>
      </div>
    </form>

    <div class="mt-6 text-center text-sm">
      <a href="/login" class="text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors">
        {$t('auth.register.to_login')}
      </a>
    </div>
  </div>
</div>
