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

  // A logged-in user visiting the login page is sent straight to the dashboard: this is a UX matter, not a security fallback —
  // the CSRF layer already grades by "does this request carry a valid session", and validates with a session-bound token when logged in, so it no longer 403s.
  // Uses $effect rather than onMount: on a full page load initAuth() is async, so the auth state is not ready yet at onMount time.
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
        // First factor passed, second factor required: enter the SPA's second step (protocol GET/POST /api/v1/auth/totp).
        // The second-step credential is an HttpOnly cookie the server issues in this response, so navigation must happen right away.
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

    <!-- Optional OIDC login: the component probes the server config on mount and renders the entry only when enabled -->
    <OIDCLoginEntry />
  </div>
</Page>
