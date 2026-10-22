<script lang="ts">
  import { onMount } from 'svelte';
  import { routeStore, initRouter, navigate, viewKey } from './lib/router';
  import { localeStore } from './lib/i18n';
  import { initAuth, clearSession } from './lib/auth';
  import { apiClient } from './lib/api';
  import { appVersion } from './lib/version';
  import AppShell from './lib/components/shell/AppShell.svelte';
  import Toaster from './lib/components/ui/Toaster.svelte';
  import ConfirmHost from './lib/components/ui/ConfirmHost.svelte';
  import NotFoundView from './lib/views/NotFoundView.svelte';

  // The app version comes from a meta tag the server injects into <head> of the entry; it is empty in dev mode or static previews and the footer hides it.
  const version = appVersion();

  // Session invalidated mid-way (401, or the CSRF check on the post-login endpoint finding no session): clear auth state and send back to the login page.
  // Before the fix nothing happened here - the UI kept showing "logged in", every action failed, and only a manual refresh recovered it.
  // Do not redirect when already on an auth page, so the login/registration/bootstrap error messages themselves are not interrupted.
  const AUTH_PAGES = ['/login', '/login/totp', '/register', '/setup'];
  apiClient.onUnauthorized = () => {
    clearSession();
    const path = typeof window !== 'undefined' ? window.location.pathname : '';
    if (!AUTH_PAGES.includes(path)) {
      navigate('/login');
    }
  };

  // Initialize the auth session and browser routing listeners on mount (popstate and link proxying)
  onMount(() => {
    initAuth();
    const cleanup = initRouter();
    return cleanup;
  });

  // Sync the current language to the lang attribute on the HTML root element
  $effect(() => {
    if (typeof document !== 'undefined') {
      document.documentElement.lang = $localeStore;
    }
  });

  const ActiveComponent = $derived(
    $routeStore.route ? $routeStore.route.component : (NotFoundView as any)
  );

  // Establish a component rebuild boundary per route: different entities of the same component (deck A→B, note A→B) get a brand-new instance,
  // so views that fetch only on mount do not keep the previous entity's title, form and write-request target.
  const activeKey = $derived(viewKey($routeStore));
</script>

<AppShell {version}>
  {#key activeKey}
    <ActiveComponent />
  {/key}
</AppShell>
<Toaster />
<ConfirmHost />
