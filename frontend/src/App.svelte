<script lang="ts">
  import { onMount } from 'svelte';
  import { routeStore, initRouter, navigate, viewKey } from './lib/router';
  import { localeStore, t } from './lib/i18n';
  import { initAuth, clearSession } from './lib/auth';
  import { apiClient } from './lib/api';
  import { appVersion } from './lib/version';
  import NavHeader from './lib/components/NavHeader.svelte';
  import NotFoundView from './lib/views/NotFoundView.svelte';

  // 程序版本由服务端注入入口 <head> 的 meta 提供；开发模式或静态预览下为空，页脚不显示。
  const version = appVersion();

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

  // 按路由建立组件重建边界：同一组件的不同实体（卡组 A→B、笔记 A→B）拿到全新实例，
  // 只在挂载时取数的视图不会保留上一实体的标题、表单与写请求目标。
  const activeKey = $derived(viewKey($routeStore));
</script>

<div class="min-h-screen flex flex-col bg-zinc-50 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100">
  <NavHeader />

  <main class="flex-1">
    {#key activeKey}
      <ActiveComponent />
    {/key}
  </main>

  <footer class="border-t border-zinc-200 dark:border-zinc-800 py-6 text-center text-xs text-zinc-400 dark:text-zinc-500">
    <div class="flex items-center justify-center gap-2">
      <a
        href="https://github.com/bestnite/engram"
        target="_blank"
        rel="noreferrer noopener"
        class="font-medium hover:text-zinc-600 dark:hover:text-zinc-300 transition-colors"
      >
        {$t('app.name')}
      </a>
      {#if version}
        <span aria-hidden="true">·</span>
        <span>{version}</span>
      {/if}
    </div>
  </footer>
</div>
