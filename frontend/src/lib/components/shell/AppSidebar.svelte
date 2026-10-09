<script lang="ts">
  import type { Component } from 'svelte';
  import {
    House,
    CirclePlay,
    ChartColumn,
    Layers,
    Upload,
    SlidersHorizontal,
    Shield,
    Settings,
    PanelLeftClose,
    PanelLeftOpen,
  } from '@lucide/svelte';
  import { t } from '../../i18n';
  import { routeStore } from '../../router';
  import { authStore } from '../../auth';
  import { cn } from '../ui/utils';
  import AppLogo from './AppLogo.svelte';
  import UserMenu from './UserMenu.svelte';
  import { sidebarDecks } from './sidebar';

  /**
   * 应用主导航（侧边栏）。桌面端常驻左侧，可收起成只有图标的窄栏；移动端放进抽屉里，
   * 由 AppShell 决定放在哪里，本组件只负责内容。
   *
   * 导航项按用途分组（学习 / 内容），卡组下直接列出待复习最多的几个卡组：
   * 切换卡组只需一次点击，不必先回到卡组列表。
   */
  interface Props {
    collapsed?: boolean;
    /** 在抽屉里时不显示收起按钮：抽屉有自己的关闭方式。 */
    inDrawer?: boolean;
    onToggleCollapse?: () => void;
    version?: string;
  }

  let { collapsed = false, inDrawer = false, onToggleCollapse, version = '' }: Props = $props();

  interface NavItem {
    href: string;
    label: string;
    icon: Component<{ class?: string; 'aria-hidden'?: boolean | 'true' | 'false' }>;
    /** 右侧数字（待复习数）；null 不显示。 */
    count?: number | null;
    active: boolean;
  }

  const path = $derived($routeStore.path);
  const isAdmin = $derived($authStore.user?.role === 'admin');
  const decks = $derived($sidebarDecks.decks);
  const totalDue = $derived($sidebarDecks.totalDue);

  // 当前卡组：/decks/:id 及其子页面（设置、共享、笔记编辑）都算在这个卡组里。
  const currentDeckId = $derived.by(() => {
    const m = /^\/decks\/([^/]+)/.exec(path);
    return m ? decodeURIComponent(m[1] ?? '') : null;
  });
  const currentDeckListed = $derived(currentDeckId !== null && decks.some((d) => d.id === currentDeckId));

  const studyItems = $derived<NavItem[]>([
    { href: '/', label: $t('nav.today'), icon: House, active: path === '/' },
    { href: '/review', label: $t('nav.review'), icon: CirclePlay, count: totalDue, active: path.startsWith('/review') },
    { href: '/stats', label: $t('nav.stats'), icon: ChartColumn, active: path.startsWith('/stats') },
  ]);

  // 「卡组」本身只在卡组列表页、或当前卡组没有列在下面时高亮，避免父子同时高亮。
  const decksItem = $derived<NavItem>({
    href: '/decks',
    label: $t('nav.decks'),
    icon: Layers,
    active: path === '/decks' || (path.startsWith('/decks/') && !currentDeckListed),
  });

  const libraryItems = $derived<NavItem[]>([
    { href: '/import', label: $t('sidebar.import'), icon: Upload, active: path.startsWith('/import') },
    { href: '/presets', label: $t('nav.presets'), icon: SlidersHorizontal, active: path.startsWith('/presets') },
  ]);

  // 管理入口只给管理员（与服务端的管理权限判据一致），避免普通用户点进去吃 403。
  const footerItems = $derived<NavItem[]>([
    // /admin 没有概览页，直接指向管理导航的第一项。
    ...(isAdmin ? [{ href: '/admin/users', label: $t('nav.admin'), icon: Shield, active: path.startsWith('/admin') }] : []),
    { href: '/settings', label: $t('nav.settings'), icon: Settings, active: path.startsWith('/settings') },
  ]);

  const itemClass = (active: boolean) =>
    cn(
      'group flex h-8.5 items-center gap-2.5 rounded-md text-sm transition-colors',
      collapsed ? 'w-10 justify-center' : 'px-2.5',
      active ? 'bg-selected font-medium text-foreground' : 'text-foreground/75 hover:bg-muted hover:text-foreground'
    );
</script>

{#snippet navLink(item: NavItem)}
  <a
    href={item.href}
    class={itemClass(item.active)}
    aria-current={item.active ? 'page' : undefined}
    title={collapsed ? item.label : undefined}
  >
    <item.icon class="size-4 shrink-0" aria-hidden="true" />
    <span class={collapsed ? 'sr-only' : 'truncate'}>{item.label}</span>
    {#if item.count != null && item.count > 0}
      {#if collapsed}
        <span class="sr-only">{$t('sidebar.due_count', { count: item.count })}</span>
      {:else}
        <span class="ml-auto text-xs font-medium tabular-nums text-destructive-foreground" aria-hidden="true">{item.count}</span>
        <span class="sr-only">{$t('sidebar.due_count', { count: item.count })}</span>
      {/if}
    {/if}
  </a>
{/snippet}

{#snippet groupLabel(text: string)}
  {#if collapsed}
    <div class="mx-auto my-2 h-px w-6 bg-sidebar-border" aria-hidden="true"></div>
  {:else}
    <div class="px-2.5 pb-1 pt-4 text-xs text-muted-foreground">{text}</div>
  {/if}
{/snippet}

<nav
  aria-label={$t('sidebar.label')}
  class={cn('flex h-full flex-col gap-0.5 overflow-y-auto overflow-x-hidden px-2.5 py-3', collapsed && 'items-center px-3')}
  data-testid="app-sidebar"
>
  <div class={cn('mb-2 flex h-9 items-center', collapsed ? 'justify-center' : 'justify-between pl-1.5')}>
    {#if !collapsed}
      <AppLogo />
    {/if}
    {#if !inDrawer && onToggleCollapse}
      <button
        type="button"
        onclick={onToggleCollapse}
        class="inline-flex size-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground cursor-pointer"
        aria-label={collapsed ? $t('sidebar.expand') : $t('sidebar.collapse')}
        title={collapsed ? $t('sidebar.expand') : $t('sidebar.collapse')}
        data-testid="sidebar-collapse-toggle"
      >
        {#if collapsed}
          <PanelLeftOpen class="size-4" aria-hidden="true" />
        {:else}
          <PanelLeftClose class="size-4" aria-hidden="true" />
        {/if}
      </button>
    {/if}
  </div>

  {@render groupLabel($t('sidebar.group.study'))}
  {#each studyItems as item (item.href)}
    {@render navLink(item)}
  {/each}

  {@render groupLabel($t('sidebar.group.library'))}
  {@render navLink(decksItem)}
  {#if !collapsed && decks.length > 0}
    <div class="ml-[1.0625rem] flex flex-col gap-0.5 border-l border-sidebar-border pl-2" data-testid="sidebar-decks">
      {#each decks as deck (deck.id)}
        {@const active = currentDeckId === deck.id}
        <a
          href="/decks/{encodeURIComponent(deck.id)}"
          class={cn(
            'flex h-8 items-center gap-2 rounded-md px-2.5 text-[13px] transition-colors',
            active ? 'bg-selected font-medium text-foreground' : 'text-foreground/70 hover:bg-muted hover:text-foreground'
          )}
          aria-current={active ? 'page' : undefined}
        >
          <span class="truncate">{deck.name}</span>
          {#if deck.due}
            <span class="ml-auto text-xs tabular-nums text-muted-foreground" aria-hidden="true">{deck.due}</span>
            <span class="sr-only">{$t('sidebar.due_count', { count: deck.due })}</span>
          {/if}
        </a>
      {/each}
      <a
        href="/decks"
        class="flex h-8 items-center rounded-md px-2.5 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      >
        {$t('sidebar.all_decks')}
      </a>
    </div>
  {/if}
  {#each libraryItems as item (item.href)}
    {@render navLink(item)}
  {/each}

  <div class="min-h-6 flex-1"></div>

  {#each footerItems as item (item.href)}
    {@render navLink(item)}
  {/each}

  <div class={cn('mt-2 border-t border-sidebar-border pt-2', collapsed ? 'w-10' : 'w-full')}>
    <UserMenu {collapsed} {version} />
  </div>
</nav>
