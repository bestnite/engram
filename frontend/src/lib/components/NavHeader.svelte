<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { routeStore, navigate } from '../router';
  import { authStore, logout } from '../auth';
  import { isDark, toggleTheme } from '../theme';
  import LanguageSwitcher from './LanguageSwitcher.svelte';
  import Button from './ui/Button.svelte';

  let logoutLoading = $state(false);

  // 主题按钮的图标要反映**当前**状态，所以挂载时从 DOM 读一次实际值
  // （首帧由服务端注入的内联引导设好 .dark，这里只读不改，避免覆盖用户选择）。
  let dark = $state(false);
  onMount(() => {
    dark = isDark();
  });

  function handleThemeToggle(): void {
    dark = toggleTheme();
  }

  async function handleLogout(): Promise<void> {
    logoutLoading = true;
    try {
      await logout();
      navigate('/login');
    } catch {
      // error handled in store
    } finally {
      logoutLoading = false;
    }
  }
</script>

<header class="border-b border-zinc-200 dark:border-zinc-800 bg-white/80 dark:bg-zinc-950/80 backdrop-blur-md sticky top-0 z-20">
  <div class="max-w-5xl mx-auto px-4 h-14 flex items-center justify-between">
    <div class="flex items-center space-x-6">
      <a href="/" class="flex items-center space-x-2 text-base font-bold text-zinc-900 dark:text-zinc-100 tracking-tight">
        <!-- 项目图标：与 static/icons/icon.svg（favicon/PWA 图标）同一形状——三张叠放的圆角卡。
             这里内联而不是 <img>：图标要随应用主题变色，而 icon.svg 内部用的是
             prefers-color-scheme，与本应用由 .dark 类驱动的主题不同步（浅色模式 + 深色系统会瞎）。
             三档填充与 icon.svg 一致：浅色蓝渐变 / 深色白 + 递减不透明度。 -->
        <svg class="w-5 h-5" viewBox="0 0 512 512" aria-hidden="true">
          <rect class="fill-blue-300 dark:fill-white opacity-100 dark:opacity-50" x="184" y="140" width="192" height="140" rx="26" />
          <rect class="fill-blue-400 dark:fill-white opacity-100 dark:opacity-75" x="160" y="178" width="192" height="140" rx="26" />
          <rect class="fill-blue-600 dark:fill-white" x="136" y="216" width="192" height="140" rx="26" />
        </svg>
        <span>{$t('app.name')}</span>
      </a>

      <nav class="hidden md:flex items-center space-x-1" aria-label="Main Navigation">
        <a
          href="/"
          class="px-3 py-1.5 rounded-md text-sm font-medium transition-colors {$routeStore.path === '/' ? 'text-zinc-900 dark:text-zinc-100 bg-zinc-100 dark:bg-zinc-900 font-semibold' : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200'}"
        >
          {$t('nav.today')}
        </a>
        <a
          href="/decks"
          class="px-3 py-1.5 rounded-md text-sm font-medium transition-colors {$routeStore.path.startsWith('/decks') ? 'text-zinc-900 dark:text-zinc-100 bg-zinc-100 dark:bg-zinc-900 font-semibold' : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200'}"
        >
          {$t('nav.decks')}
        </a>
        <a
          href="/review"
          class="px-3 py-1.5 rounded-md text-sm font-medium transition-colors {$routeStore.path.startsWith('/review') ? 'text-zinc-900 dark:text-zinc-100 bg-zinc-100 dark:bg-zinc-900 font-semibold' : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200'}"
        >
          {$t('nav.review')}
        </a>
        <a
          href="/stats"
          class="px-3 py-1.5 rounded-md text-sm font-medium transition-colors {$routeStore.path.startsWith('/stats') ? 'text-zinc-900 dark:text-zinc-100 bg-zinc-100 dark:bg-zinc-900 font-semibold' : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200'}"
        >
          {$t('nav.stats')}
        </a>
        <a
          href="/settings/keys"
          class="px-3 py-1.5 rounded-md text-sm font-medium transition-colors {$routeStore.path.startsWith('/settings/keys') ? 'text-zinc-900 dark:text-zinc-100 bg-zinc-100 dark:bg-zinc-900 font-semibold' : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200'}"
        >
          {$t('nav.api_keys')}
        </a>
        {#if $authStore.authenticated}
          <!-- 管理入口只给管理员（与 SSR mainNav 同一判据），避免普通用户点进去吃 403 -->
          {#if $authStore.user?.role === 'admin'}
            <a
              href="/admin"
              class="px-3 py-1.5 rounded-md text-sm font-medium transition-colors {$routeStore.path.startsWith('/admin') ? 'text-zinc-900 dark:text-zinc-100 bg-zinc-100 dark:bg-zinc-900 font-semibold' : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200'}"
            >
              {$t('nav.admin')}
            </a>
          {/if}
          <!-- 预设与个人设置对每个已登录用户可见（与 SSR mainNav 同序） -->
          <a
            href="/presets"
            class="px-3 py-1.5 rounded-md text-sm font-medium transition-colors {$routeStore.path.startsWith('/presets') ? 'text-zinc-900 dark:text-zinc-100 bg-zinc-100 dark:bg-zinc-900 font-semibold' : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200'}"
          >
            {$t('nav.presets')}
          </a>
        {/if}
        <a
          href="/settings"
          class="px-3 py-1.5 rounded-md text-sm font-medium transition-colors {$routeStore.path.startsWith('/settings') && !$routeStore.path.startsWith('/settings/keys') ? 'text-zinc-900 dark:text-zinc-100 bg-zinc-100 dark:bg-zinc-900 font-semibold' : 'text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-200'}"
        >
          {$t('nav.settings')}
        </a>
      </nav>
    </div>

    <div class="flex items-center space-x-3">
      <!-- 深浅主题切换。刻意**不加** data-theme-toggle：pwa.js 的加载期绑定会往该属性上写
           btn.onclick，与 Svelte 的 addEventListener 叠加会在一次点击里切换两次（看起来没反应）。
           本按钮由 theme.ts 全权处理。 -->
      <button
        type="button"
        data-testid="nav-theme-toggle"
        title={$t('nav.toggle_theme')}
        aria-label={$t('nav.toggle_theme')}
        onclick={handleThemeToggle}
        class="p-2 rounded-lg border border-zinc-200 dark:border-zinc-700 bg-white dark:bg-zinc-800 text-zinc-600 dark:text-zinc-300 hover:bg-zinc-50 dark:hover:bg-zinc-700 transition-colors btn-press cursor-pointer"
      >
        {#if dark}
          <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <circle cx="12" cy="12" r="4" />
            <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
          </svg>
        {:else}
          <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" />
          </svg>
        {/if}
      </button>

      <!-- 语言入口对所有人生效：未登录时把选择写进地址的 ?lang（没有账号可落库），
           已登录时走 PATCH /api/v1/settings/locale 写 users.locale，与 /settings 的语言字段同一列。
           两处共用同一份 locale store，因此界面永远只有一种语言。 -->
      <LanguageSwitcher />

      {#if $authStore.authenticated && $authStore.user}
        <div class="flex items-center space-x-2 text-sm">
          <span class="text-zinc-600 dark:text-zinc-400 font-medium hidden sm:inline">
            {$authStore.user.display_name || $authStore.user.username}
          </span>
          <button
            type="button"
            data-testid="nav-logout-btn"
            disabled={logoutLoading}
            onclick={handleLogout}
            class="px-2.5 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-700 bg-white dark:bg-zinc-800 text-zinc-700 dark:text-zinc-200 hover:bg-zinc-50 dark:hover:bg-zinc-700 text-xs font-medium transition-colors btn-press cursor-pointer disabled:opacity-50"
          >
            {$t('nav.logout')}
          </button>
        </div>
      {:else}
        <Button variant="primary" size="sm" href="/login"
          testId="nav-login-link"
        >
          {$t('nav.login')}
        </Button>
      {/if}
    </div>
  </div>
</header>
