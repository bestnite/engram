<script lang="ts">
  import { t } from '../i18n';
  import { navigate } from '../router';
  import { setup } from '../auth';
  import { getApiErrorMessageKey, validatePasswordConfirmation, ApiClientError } from '../api';
  import Button from '../components/ui/Button.svelte';

  let username = $state('');
  let email = $state('');
  let displayName = $state('');
  let password = $state('');
  let confirmPassword = $state('');
  let loading = $state(false);
  let errorKey = $state<string | null>(null);

  async function handleSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!username.trim() || !password || !confirmPassword) {
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
      await setup({
        username: username.trim(),
        email: email.trim(),
        display_name: displayName.trim(),
        password,
      });
      // 引导不建立会话：与 SSR 一致，成功后回到登录页。
      navigate('/login');
    } catch (err) {
      if (err instanceof ApiClientError && err.status === 404) {
        // 已有活跃管理员：引导窗口已关闭（GET /setup 也会 404）。
        errorKey = 'auth.setup.unavailable';
      } else {
        errorKey = err instanceof ApiClientError ? getApiErrorMessageKey(err) : 'error.unknown';
      }
    } finally {
      loading = false;
    }
  }
</script>

<div class="py-12 max-w-md mx-auto px-4">
  <div class="card-elevated p-8 rounded-xl">
    <div class="mb-6 text-center">
      <h1 class="text-2xl font-bold tracking-tight text-foreground">
        {$t('auth.setup.heading')}
      </h1>
      <p class="mt-2 text-sm text-muted-foreground">
        {$t('auth.setup.intro')}
      </p>
    </div>

    {#if errorKey}
      <div
        data-testid="setup-error"
        class="mb-6 p-4 rounded-lg bg-rose-50 dark:bg-rose-950/40 border border-rose-200 dark:border-rose-800/60 text-rose-700 dark:text-rose-300 text-sm"
      >
        <span>{$t(errorKey)}</span>
      </div>
    {/if}

    <form onsubmit={handleSubmit} class="space-y-4">
      <div>
        <label for="setup-username" class="block text-sm font-medium text-foreground/80 mb-1.5">
          {$t('auth.field.username')}
        </label>
        <input
          id="setup-username"
          data-testid="setup-username"
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
        <label for="setup-email" class="block text-sm font-medium text-foreground/80 mb-1.5">
          {$t('auth.field.email')}
        </label>
        <input
          id="setup-email"
          data-testid="setup-email"
          name="email"
          type="email"
          autocomplete="email"
          bind:value={email}
          disabled={loading}
          class="field-input text-sm w-full transition-colors disabled:opacity-50"
        />
      </div>

      <div>
        <label for="setup-display-name" class="block text-sm font-medium text-foreground/80 mb-1.5">
          {$t('auth.field.display_name')}
        </label>
        <input
          id="setup-display-name"
          data-testid="setup-display-name"
          name="display_name"
          type="text"
          autocomplete="name"
          bind:value={displayName}
          disabled={loading}
          class="field-input text-sm w-full transition-colors disabled:opacity-50"
        />
      </div>

      <div>
        <label for="setup-password" class="block text-sm font-medium text-foreground/80 mb-1.5">
          {$t('auth.field.password')}
        </label>
        <input
          id="setup-password"
          data-testid="setup-password"
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
        <label for="setup-password-confirm" class="block text-sm font-medium text-foreground/80 mb-1.5">
          {$t('auth.field.password_confirm')}
        </label>
        <input
          id="setup-password-confirm"
          data-testid="setup-password-confirm"
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
        <Button type="submit" disabled={loading || !username.trim() || !password || !confirmPassword} variant="primary" size="lg" class="w-full" testId="setup-submit">
          {#if loading}
            <div class="w-4 h-4 border-2 border-current border-t-transparent rounded-full animate-spin" aria-hidden="true"></div>
            <span>{$t('auth.setup.submitting')}</span>
          {:else}
            <span>{$t('auth.setup.submit')}</span>
          {/if}
        </Button>
      </div>
    </form>
  </div>
</div>
