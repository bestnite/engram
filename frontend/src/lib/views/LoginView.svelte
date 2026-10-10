<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { t } from '../i18n';
  import { navigate } from '../router';
  import { login, authStore } from '../auth';
  import { getApiErrorMessageKey, ApiClientError } from '../api';
  import OIDCLoginEntry from '../components/OIDCLoginEntry.svelte';
  import Button from '../components/ui/Button.svelte';
  import { toast } from '../components/ui/toast';

  let username = $state('');
  let password = $state('');
  let loading = $state(false);

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
        toast.error($t(getApiErrorMessageKey(err)));
      } else {
        toast.error($t('error.unknown'));
      }
    } finally {
      loading = false;
    }
  }
</script>

<Page width="narrow">
  <div>
    <div class="mb-6 text-center">
      <h1 class="text-2xl font-semibold tracking-tight text-foreground">
        {$t('auth.login.heading')}
      </h1>
    </div>

    <form onsubmit={handleSubmit} class="space-y-4">
      <div>
        <label for="login-username" class="block text-sm font-medium text-foreground/80 mb-1.5">
          {$t('auth.field.username')}
        </label>
        <input
          id="login-username"
          data-testid="login-username"
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
        <label for="login-password" class="block text-sm font-medium text-foreground/80 mb-1.5">
          {$t('auth.field.password')}
        </label>
        <input
          id="login-password"
          data-testid="login-password"
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
        <Button type="submit" {loading} disabled={!username.trim() || !password} variant="primary" size="lg" class="w-full" testId="login-submit">
          {$t('auth.login.submit')}
        </Button>
      </div>
    </form>

    <!-- OIDC 可选登录：组件挂载时探测服务端配置，仅在 enabled 时渲染入口 -->
    <OIDCLoginEntry />
  </div>
</Page>
