<script lang="ts">
  import type { Snippet } from 'svelte';
  import { Dialog as BitsDialog } from 'bits-ui';
  import { Menu, X } from '@lucide/svelte';
  import { t } from '../../i18n';
  import { routeStore } from '../../router';
  import { authStore } from '../../auth';
  import { apiClient } from '../../api';
  import { cn } from '../ui/utils';
  import Button from '../ui/Button.svelte';
  import LanguageSwitcher from '../LanguageSwitcher.svelte';
  import ThemeToggle from './ThemeToggle.svelte';
  import AppLogo from './AppLogo.svelte';
  import AppSidebar from './AppSidebar.svelte';
  import { sidebarCollapsed, setSidebarCollapsed, refreshSidebarDecks, handleDataChanged } from './sidebar';

  /**
   * App shell: picks one of three layouts by route and login state.
   *
   * - sidebar: regular pages when logged in. On desktop the sidebar is persistent (collapsible into an icon rail); on narrow screens it becomes a top bar + drawer.
   * - focus: the review page. Only a thin top bar and "exit" are kept so navigation does not distract.
   * - bare: logged-out pages such as login, registration and password recovery, plus public pages accessed while logged out (share browsing, unsubscribe).
   *
   * While the session is still loading it lays out as "logged in": the vast majority of visits come from logged-in users, and drawing the sidebar first and then switching away
   * in the rare cases causes far less jank than jumping from no sidebar to a sidebar on every load.
   */
  interface Props {
    version?: string;
    children: Snippet;
  }

  let { version = '', children }: Props = $props();

  // These pages hide app navigation whether or not you are logged in: they are steps before entering the app.
  const BARE_ROUTES = new Set([
    'login',
    'totp-login',
    'register',
    'setup',
    'forgot-password',
    'reset-password',
    'verify-email',
    'confirm-email-change',
    'unsubscribe',
  ]);
  const FOCUS_ROUTES = new Set(['review']);

  const routeName = $derived($routeStore.route?.name ?? '');
  const signedOut = $derived($authStore.initialized && !$authStore.authenticated);
  const layout = $derived<'sidebar' | 'focus' | 'bare'>(
    BARE_ROUTES.has(routeName) || signedOut ? 'bare' : FOCUS_ROUTES.has(routeName) ? 'focus' : 'sidebar'
  );

  let drawerOpen = $state(false);

  // The sidebar's deck list and due counts stay in sync with writes: creating, deleting or renaming decks in a page, or adding/removing cards,
  // never changes the route, and refreshing only "when the page changes" would keep showing a stale list until the whole page is reloaded.
  apiClient.onDataChanged = (kind) => {
    if ($authStore.authenticated) handleDataChanged(apiClient, kind);
  };

  // Changing page closes the drawer: links inside the drawer are handled by the global route proxy and do not trigger closing themselves.
  // At the same time it refreshes the sidebar's per-deck due counts (throttled), so after reviewing or adding/removing decks the new numbers are there when you reach another page.
  $effect(() => {
    void $routeStore.path;
    drawerOpen = false;
    if ($authStore.authenticated) {
      void refreshSidebarDecks(apiClient);
    }
  });
</script>

{#if layout === 'sidebar'}
  <div class="flex min-h-screen bg-background text-foreground" style="--app-sidebar-w: {$sidebarCollapsed ? '4rem' : '15rem'}">
    <aside
      class={cn(
        'app-sidebar sticky top-0 hidden h-screen shrink-0 border-r border-sidebar-border bg-sidebar transition-[width] duration-200 ease-out md:block',
        $sidebarCollapsed ? 'w-16' : 'w-60'
      )}
    >
      <AppSidebar collapsed={$sidebarCollapsed} onToggleCollapse={() => setSidebarCollapsed(!$sidebarCollapsed)} {version} />
    </aside>

    <div class="flex min-w-0 flex-1 flex-col">
      <!-- 窄屏顶栏：侧边栏收进抽屉，这里只留打开按钮与品牌。 -->
      <header
        class="sticky top-0 z-30 flex h-14 items-center gap-2 border-b border-border bg-background/85 px-3 backdrop-blur-md md:hidden"
      >
        <BitsDialog.Root bind:open={drawerOpen}>
          <BitsDialog.Trigger
            class="inline-flex size-9 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground cursor-pointer"
            aria-label={$t('sidebar.open')}
            data-testid="sidebar-drawer-trigger"
          >
            <Menu class="size-5" aria-hidden="true" />
          </BitsDialog.Trigger>
          <BitsDialog.Portal>
            <BitsDialog.Overlay
              class="fixed inset-0 z-50 bg-overlay data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=closed]:animate-out data-[state=closed]:fade-out-0"
            />
            <BitsDialog.Content
              class="fixed inset-y-0 left-0 z-50 w-72 max-w-[85vw] border-r border-sidebar-border bg-sidebar shadow-xl duration-200 data-[state=open]:animate-in data-[state=open]:slide-in-from-left data-[state=closed]:animate-out data-[state=closed]:slide-out-to-left"
            >
              <BitsDialog.Title class="sr-only">{$t('sidebar.label')}</BitsDialog.Title>
              <AppSidebar inDrawer {version} />
            </BitsDialog.Content>
          </BitsDialog.Portal>
        </BitsDialog.Root>
        <AppLogo />
      </header>

      <main class="min-w-0 flex-1">
        {@render children()}
      </main>
    </div>
  </div>
{:else if layout === 'focus'}
  <div class="flex min-h-screen flex-col bg-background text-foreground">
    <header class="flex h-14 items-center justify-between border-b border-border px-4">
      <AppLogo />
      <a
        href="/"
        class="inline-flex h-8 items-center gap-1.5 rounded-md px-2.5 text-[13px] text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        data-testid="focus-exit"
      >
        <X class="size-4" aria-hidden="true" />
        {$t('sidebar.exit_review')}
      </a>
    </header>
    <main class="min-w-0 flex-1">
      {@render children()}
    </main>
  </div>
{:else}
  <div class="flex min-h-screen flex-col bg-background text-foreground">
    <header class="flex h-14 items-center justify-between gap-3 px-4 sm:px-6">
      <AppLogo />
      <div class="flex items-center gap-2">
        <ThemeToggle />
        <LanguageSwitcher />
        {#if !$authStore.authenticated}
          <Button variant="primary" size="sm" href="/login" testId="nav-login-link">
            {$t('nav.login')}
          </Button>
        {/if}
      </div>
    </header>
    <main class="min-w-0 flex-1">
      {@render children()}
    </main>
    <footer class="py-6 text-center text-xs text-muted-foreground">
      <a
        href="https://github.com/bestnite/engram"
        target="_blank"
        rel="noreferrer noopener"
        class="font-medium transition-colors hover:text-foreground"
      >
        {$t('app.name')}
      </a>
      {#if version}
        <span aria-hidden="true"> · </span>
        <span>{version}</span>
      {/if}
    </footer>
  </div>
{/if}
