<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { navigate } from '../router';
  import { register, authStore } from '../auth';
  import { apiClient, getApiErrorMessageKey, validatePasswordConfirmation, ApiClientError } from '../api';
  import type { RegistrationPolicy } from '../api';
  import Button from '../components/ui/Button.svelte';
  import { toast } from '../components/ui/toast';

  // The registration page's first-frame shape is decided by the site policy, so the form must not render before the probe settles: otherwise a closed site would flash the
  // form first and then have it replaced by the blocked panel. 'probing' = probing; 'unknown' = the probe failed, or this visit is not subject to the policy
  // (it carries an invite token) — both fall through to the form, and whether it is allowed is ultimately decided by the server at submission time.
  type RegistrationState = 'probing' | 'unknown' | RegistrationPolicy;

  // The optional props.initialPolicy lets server-side rendering and tests inject a known state, avoiding a first-frame flash; in production it is driven by the probe result.
  interface Props {
    initialPolicy?: RegistrationState;
  }

  // svelte-ignore state_referenced_locally
  let { initialPolicy = 'probing' }: Props = $props();

  // The invite token comes in through the URL query parameter (GET /register?invite=... serves the app shell).
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
  // Only holds local validation errors from before submission (the two passwords do not match); request failures go through toast.
  let errorKey = $state<string | null>(null);
  // A visit carrying an invite token goes through the invite acceptance path, independent of policy (the invite policy is exactly what lets it through), so from the start it is treated as
  // "not subject to the policy" and handed straight to the form.
  // svelte-ignore state_referenced_locally
  let registrationState = $state<RegistrationState>(invite ? 'unknown' : initialPolicy);

  // A logged-in user visiting the registration page is sent straight to the dashboard: this is a UX matter, not a security fallback —
  // the CSRF layer already grades by "does this request carry a valid session", and validates with a session-bound token when logged in, so it no longer 403s.
  // Uses $effect rather than onMount: on a full page load initAuth() is async, so the auth state is not ready yet at onMount time.
  $effect(() => {
    if ($authStore.initialized && $authStore.authenticated) {
      navigate('/');
    }
  });

  // Policy probe: read-only, callable anonymously. No probing is needed with an invite token (the invite path is independent of policy).
  onMount(async () => {
    if (registrationState !== 'probing') {
      return;
    }
    try {
      registrationState = (await apiClient.registrationInfo()).policy;
    } catch {
      // A failed probe equals "policy unknown": hand over the form and let the server decide at submission time.
      registrationState = 'unknown';
    }
  });

  // Copy for why this visit cannot self-register; null means the form can be filled in and submitted.
  // The criterion matches the server's attemptRegistration: with an invite token only the token is looked at, without one only the policy is.
  const blockedReason = $derived(
    invite
      ? null
      : registrationState === 'closed'
        ? 'auth.error.registration_closed'
        : registrationState === 'invite'
          ? 'auth.error.invite_required'
          : null
  );

  async function handleSubmit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!username.trim() || !email.trim() || !password || !confirmPassword) {
      return;
    }
    // The two entries not matching is purely a frontend convention: the server takes only one password, so no request is submitted here.
    const confirmation = validatePasswordConfirmation(password, confirmPassword);
    if (!confirmation.valid) {
      errorKey = confirmation.errorKey ?? 'error.unknown';
      return;
    }
    errorKey = null;
    loading = true;
    try {
      await register({
        username: username.trim(),
        email: email.trim(),
        display_name: displayName.trim(),
        password,
        invite,
      });
      // Registration does not establish a session: consistent with SSR, it returns to the login page on success.
      navigate('/login');
    } catch (err) {
      toast.error($t(err instanceof ApiClientError ? getApiErrorMessageKey(err) : 'error.unknown'));
    } finally {
      loading = false;
    }
  }
</script>

<Page width="narrow">
{#if registrationState === 'probing'}
  <div>
    <div data-testid="register-probing">
      <div class="flex items-center justify-center gap-2 text-sm text-muted-foreground">
        <div class="w-4 h-4 border-2 border-current border-t-transparent rounded-full animate-spin" aria-hidden="true"></div>
        <span>{$t('common.loading')}</span>
      </div>
    </div>
  </div>
{:else if blockedReason}
  <div>
    <div data-testid="register-blocked">
      <div class="mb-6 text-center">
        <h1 class="text-2xl font-semibold tracking-tight text-foreground">
          {$t('auth.register.blocked_heading')}
        </h1>
      </div>

      <p class="mb-6 text-center text-sm text-muted-foreground">
        {$t(blockedReason)}
      </p>

      <a
        href="/login"
        class="block text-center text-sm text-muted-foreground hover:text-foreground transition-colors"
      >
        {$t('auth.register.to_login')}
      </a>
    </div>
  </div>
{:else}
  <div>
    <div>
      <div class="mb-6 text-center">
        <h1 class="text-2xl font-semibold tracking-tight text-foreground">
          {$t('auth.register.heading')}
        </h1>
      </div>

      {#if invite}
        <p class="mb-6 text-sm text-muted-foreground">
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
          <label for="register-username" class="block text-sm font-medium text-foreground/80 mb-1.5">
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
          <label for="register-email" class="block text-sm font-medium text-foreground/80 mb-1.5">
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
          <label for="register-display-name" class="block text-sm font-medium text-foreground/80 mb-1.5">
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
          <label for="register-password" class="block text-sm font-medium text-foreground/80 mb-1.5">
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
          <label for="register-password-confirm" class="block text-sm font-medium text-foreground/80 mb-1.5">
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
          <Button type="submit" {loading} disabled={!username.trim() || !email.trim() || !password || !confirmPassword} variant="primary" size="lg" class="w-full" testId="register-submit">
            {$t('auth.register.submit')}
          </Button>
        </div>
      </form>

      <div class="mt-6 text-center text-sm">
        <a href="/login" class="text-muted-foreground hover:text-foreground transition-colors">
          {$t('auth.register.to_login')}
        </a>
      </div>
    </div>
  </div>
{/if}
</Page>
