<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { navigate } from '../router';
  import { login, authStore } from '../auth';
  import { apiClient, getApiErrorMessageKey, ApiClientError } from '../api';
  import type { RegistrationPolicy } from '../api';
  import OIDCLoginEntry from '../components/OIDCLoginEntry.svelte';
  import Button from '../components/ui/Button.svelte';
  import { toast } from '../components/ui/toast';

  // Whether the login page can offer a link to self-service registration is decided by the site policy, so the link must not render
  // before the probe settles: otherwise a closed site would flash a link that leads to a blocked page.
  // 'probing' = probe in flight; 'unknown' = the probe failed, and the link is offered because the server decides at submission time.
  type RegistrationState = 'probing' | 'unknown' | RegistrationPolicy;

  // The optional props.initialPolicy lets server-side rendering and tests inject a known state, avoiding a first-frame flash; in production it is driven by the probe result.
  interface Props {
    initialPolicy?: RegistrationState;
  }

  // svelte-ignore state_referenced_locally
  let { initialPolicy = 'probing' }: Props = $props();

  // The initial policy is only read once to seed state; later updates come from the probe.
  // svelte-ignore state_referenced_locally
  let registrationState = $state<RegistrationState>(initialPolicy);

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

  // Policy probe: read-only, callable anonymously.
  onMount(async () => {
    if (registrationState !== 'probing') {
      return;
    }
    try {
      registrationState = (await apiClient.registrationInfo()).policy;
    } catch {
      // A failed probe equals "policy unknown": offer the link, because the server still decides at submission time.
      registrationState = 'unknown';
    }
  });

  // 'open' is the only policy that lets an anonymous visitor create an account, so only then is a generic register link useful.
  // 'unknown' also shows it: hiding a working path is worse than leading one visitor to a page that explains why it is blocked.
  // 'probing' shows nothing, so a closed site never flashes a link that immediately disappears; 'invite' shows nothing because
  // the only way in is an invite token carried by a link the visitor already has.
  const showRegisterLink = $derived(
    registrationState === 'open' || registrationState === 'unknown'
  );

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

    {#if showRegisterLink}
      <div class="mt-6 text-center text-sm">
        <a href="/register" data-testid="login-to-register" class="text-muted-foreground hover:text-foreground transition-colors">
          {$t('auth.login.to_register')}
        </a>
      </div>
    {/if}

    <!-- Optional OIDC login: the component probes the server config on mount and renders the entry only when enabled -->
    <OIDCLoginEntry />
  </div>
</Page>
