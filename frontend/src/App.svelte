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

<AppShell {version}>
  {#key activeKey}
    <ActiveComponent />
  {/key}
</AppShell>
<Toaster />
<ConfirmHost />
