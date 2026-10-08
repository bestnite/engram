<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { navigate } from '../router';
  import { register, authStore } from '../auth';
  import { apiClient, getApiErrorMessageKey, validatePasswordConfirmation, ApiClientError } from '../api';
  import type { RegistrationPolicy } from '../api';
  import Button from '../components/ui/Button.svelte';

  // 可选 props.initialPolicy 供服务端渲染与测试注入已知策略，避免首帧闪烁；生产由探测结果驱动。
  interface Props {
    initialPolicy?: RegistrationPolicy | null;
  }

  // svelte-ignore state_referenced_locally
  let { initialPolicy = null }: Props = $props();

  // 邀请 token 由 URL 查询参数带入（GET /register?invite=... 下发应用壳）。
  const invite =
    typeof window !== 'undefined'
      ? (new URLSearchParams(window.location.search).get('invite') ?? '')
      : '';

  let username = $state('');
  let email = $state('');
  let displayName = $state('');
  let password = $state('');
  let confirmPassword = $state('');
  let loading = $state(false);
  let errorKey = $state<string | null>(null);
  // 当前注册策略；null 表示尚未探测到（含探测失败）。此时照常渲染表单——是否放行最终由服务端在
  // 提交时判定，探测失败不该把一个本来可用的注册入口藏起来。
  // svelte-ignore state_referenced_locally
  let policy = $state<RegistrationPolicy | null>(initialPolicy);

  // 已登录用户访问注册页时直接送去控制台：这是体验问题，不是安全兜底——
  // CSRF 层已按「本次请求有没有有效会话」分档，已登录时用会话绑定 token 校验，不再 403。
  // 用 $effect 而不是 onMount：整页加载时 initAuth() 是异步的，onMount 时认证状态还没就绪。
  $effect(() => {
    if ($authStore.initialized && $authStore.authenticated) {
      navigate('/');
    }
  });

  // 带邀请令牌的访问走邀请接受路径，与策略无关（invite 策略正是靠它放行），因此无需探测。
  onMount(async () => {
    if (policy !== null || invite) {
      return;
    }
    try {
      policy = (await apiClient.registrationInfo()).policy;
    } catch {
      // 探测失败等同于「策略未知」：保持表单可用，最终判定交给提交时的服务端。
      policy = null;
    }
  });

  // 这次访问无法自助注册的原因文案；null 表示可以填表提交。
  // 判定口径与服务端 attemptRegistration 一致：带邀请令牌时只看令牌，不带时才看策略。
  const blockedReason = $derived(
    invite
      ? null
      : policy === 'closed'
        ? 'auth.error.registration_closed'
        : policy === 'invite'
          ? 'auth.error.invite_required'
          : null
  );

  async function handleSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!username.trim() || !email.trim() || !password || !confirmPassword) {
      return;
    }
    // 两次输入不一致是纯前端约定：服务端只收一个 password，因此这里不提交任何请求。
    const confirmation = validatePasswordConfirmation(password, confirmPassword);
    if (!confirmation.valid) {
      errorKey = confirmation.errorKey ?? 'error.unknown';
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

{#if blockedReason}
  <div class="py-12 max-w-md mx-auto px-4">
    <div class="card-elevated p-8 rounded-xl" data-testid="register-blocked">
      <div class="mb-6 text-center">
        <h1 class="text-2xl font-bold tracking-tight text-zinc-900 dark:text-zinc-100">
          {$t('auth.register.blocked_heading')}
        </h1>
      </div>

      <p class="mb-6 text-sm text-zinc-600 dark:text-zinc-400">
        {$t(blockedReason)}
      </p>

      <a
        href="/login"
        class="block text-center text-sm text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors"
      >
        {$t('auth.register.to_login')}
      </a>
    </div>
  </div>
{:else}
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
            data-testid="register-username"
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
            data-testid="register-email"
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
            data-testid="register-display-name"
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
            data-testid="register-password"
            name="password"
            type="password"
            autocomplete="new-password"
            required
            bind:value={password}
            disabled={loading}
            class="field-input text-sm w-full transition-colors disabled:opacity-50"
          />
        </div>

        <div>
          <label for="register-password-confirm" class="block text-sm font-medium text-zinc-700 dark:text-zinc-300 mb-1.5">
            {$t('auth.field.password_confirm')}
          </label>
          <input
            id="register-password-confirm"
            data-testid="register-password-confirm"
            name="password_confirm"
            type="password"
            autocomplete="new-password"
            required
            bind:value={confirmPassword}
            disabled={loading}
            class="field-input text-sm w-full transition-colors disabled:opacity-50"
          />
        </div>

        <div class="pt-2">
          <Button type="submit" disabled={loading || !username.trim() || !email.trim() || !password || !confirmPassword} variant="primary" size="lg" class="w-full" testId="register-submit">
            {#if loading}
              <div class="w-4 h-4 border-2 border-current border-t-transparent rounded-full animate-spin" aria-hidden="true"></div>
              <span>{$t('auth.register.submitting')}</span>
            {:else}
              <span>{$t('auth.register.submit')}</span>
            {/if}
          </Button>
        </div>
      </form>

      <div class="mt-6 text-center text-sm">
        <a href="/login" class="text-zinc-600 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100 transition-colors">
          {$t('auth.register.to_login')}
        </a>
      </div>
    </div>
  </div>
{/if}
