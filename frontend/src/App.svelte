<script lang="ts">
  import { onMount } from 'svelte';
  import { routeStore, initRouter, navigate } from './lib/router';
  import { localeStore, t } from './lib/i18n';
  import { initAuth, clearSession } from './lib/auth';
  import { apiClient } from './lib/api';
  import NavHeader from './lib/components/NavHeader.svelte';
  import NotFoundView from './lib/views/NotFoundView.svelte';

  // 会话中途失效（401，或登录后端点的 CSRF 校验发现没有会话）：清掉认证状态并送回登录页。
  // 修复前这里什么都不做——界面继续显示「已登录」，每个操作都失败，必须手动刷新才恢复。
  // 已经在认证页面上时不跳转，免得打断登录/注册/引导本身的错误提示。
  const AUTH_PAGES = ['/login', '/login/totp', '/register', '/setup'];
  apiClient.onUnauthorized = () => {
    clearSession();
    const path = typeof window !== 'undefined' ? window.location.pathname : '';
    if (!AUTH_PAGES.includes(path)) {
      navigate('/login');
    }
  };

  // 挂载时初始化认证会话与浏览器路由监听（popstate 与链接代理）
  onMount(() => {
    initAuth();
    const cleanup = initRouter();
    return cleanup;
  });

  // 同步当前语言至 HTML 根节点 lang 属性
  $effect(() => {
    if (typeof document !== 'undefined') {
      document.documentElement.lang = $localeStore;
    }
  });

  const ActiveComponent = $derived(
    $routeStore.route ? $routeStore.route.component : (NotFoundView as any)
  );
</script>

<div class="min-h-screen flex flex-col bg-zinc-50 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100">
  <NavHeader />

  <main class="flex-1">
    <ActiveComponent />
  </main>

  <footer class="border-t border-zinc-200 dark:border-zinc-800 py-6 text-center text-xs text-zinc-400">
    <div class="max-w-5xl mx-auto px-4 flex items-center justify-between">
      <span>{$t('app.name')}</span>
      <span>{$t('shell.status')}</span>
    </div>
  </footer>
</div>
