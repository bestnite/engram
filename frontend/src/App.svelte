<script lang="ts">
  import { onMount } from 'svelte';
  import { routeStore, initRouter } from './lib/router';
  import { localeStore, t } from './lib/i18n';
  import NavHeader from './lib/components/NavHeader.svelte';
  import NotFoundView from './lib/views/NotFoundView.svelte';

  // 挂载时初始化浏览器路由监听（popstate 与链接代理）
  onMount(() => {
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
