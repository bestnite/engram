<script lang="ts">
  import { onMount } from 'svelte';
  import { DropdownMenu } from 'bits-ui';
  import { ChevronsUpDown, Moon, Sun, LogOut, Check } from '@lucide/svelte';
  import { t, localeStore } from '../../i18n';
  import { navigate } from '../../router';
  import { authStore, logout } from '../../auth';
  import { isDark, toggleTheme } from '../../theme';
  import { cn } from '../ui/utils';
  import { menuClasses } from '../ui/variants';
  import { switchLocale } from '../language-switch';
  import { resetSidebarDecks } from './sidebar';

  /**
   * 侧边栏底部的账户菜单：显示当前用户，收纳主题、语言与退出——这些都是低频操作，
   * 放进菜单后侧边栏只留导航本身。
   */
  interface Props {
    collapsed?: boolean;
    version?: string;
  }

  let { collapsed = false, version = '' }: Props = $props();

  const user = $derived($authStore.user);
  const displayName = $derived(user ? user.display_name || user.username : '');
  const initial = $derived(displayName ? Array.from(displayName)[0]?.toUpperCase() ?? '' : '');

  // 主题菜单项要反映**当前**状态，所以挂载时从 DOM 读一次实际值
  // （首帧由服务端注入的内联引导设好 .dark，这里只读不改，避免覆盖用户选择）。
  let dark = $state(false);
  onMount(() => {
    dark = isDark();
  });

  let localeFailed = $state(false);

  async function handleLocale(value: string) {
    localeFailed = !(await switchLocale(value));
  }

  async function handleLogout(): Promise<void> {
    try {
      await logout();
      resetSidebarDecks();
      navigate('/login');
    } catch {
      // 错误已写进 authStore，由外壳决定如何展示。
    }
  }

  const locales = $derived([
    { value: 'zh-CN', label: $t('language.zh-CN') },
    { value: 'en', label: $t('language.en') },
  ]);
</script>

{#if user}
  <DropdownMenu.Root>
    <DropdownMenu.Trigger
      class={cn(
        'flex w-full items-center gap-2.5 rounded-md text-left transition-colors hover:bg-muted cursor-pointer data-[state=open]:bg-muted',
        collapsed ? 'size-10 justify-center' : 'h-11 px-1.5'
      )}
      aria-label={$t('sidebar.account')}
      data-testid="user-menu-trigger"
    >
      <span
        class="inline-flex size-7 shrink-0 items-center justify-center rounded-full bg-selected text-xs font-semibold text-foreground"
        aria-hidden="true">{initial}</span
      >
      {#if !collapsed}
        <span class="flex min-w-0 flex-1 flex-col leading-tight">
          <span class="truncate text-[13px] font-medium text-foreground">{displayName}</span>
          {#if user.email}
            <span class="truncate text-xs text-muted-foreground">{user.email}</span>
          {/if}
        </span>
        <ChevronsUpDown class="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      {/if}
    </DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <DropdownMenu.Content class={cn(menuClasses.content, 'w-56')} side={collapsed ? 'right' : 'top'} align="start" sideOffset={6}>
        <div class="px-2 py-1.5">
          <div class="truncate text-[13px] font-medium">{displayName}</div>
          {#if user.email}
            <div class="truncate text-xs text-muted-foreground">{user.email}</div>
          {/if}
        </div>
        <DropdownMenu.Separator class={menuClasses.separator} />
        <DropdownMenu.Item class={menuClasses.item} onSelect={() => (dark = toggleTheme())} data-testid="nav-theme-toggle">
          {#if dark}
            <Sun aria-hidden="true" />
            {$t('sidebar.theme_light')}
          {:else}
            <Moon aria-hidden="true" />
            {$t('sidebar.theme_dark')}
          {/if}
        </DropdownMenu.Item>
        <DropdownMenu.Separator class={menuClasses.separator} />
        <div class={menuClasses.label}>{$t('sidebar.language')}</div>
        <DropdownMenu.RadioGroup value={$localeStore} onValueChange={handleLocale}>
          {#each locales as option (option.value)}
            <DropdownMenu.RadioItem class={menuClasses.item} value={option.value} closeOnSelect={false}>
              {#snippet children({ checked })}
                <span class="flex size-4 items-center justify-center">
                  {#if checked}<Check aria-hidden="true" />{/if}
                </span>
                {option.label}
              {/snippet}
            </DropdownMenu.RadioItem>
          {/each}
        </DropdownMenu.RadioGroup>
        {#if localeFailed}
          <p role="alert" class="px-2 py-1 text-xs text-destructive-foreground">{$t('language.save_failed')}</p>
        {/if}
        <DropdownMenu.Separator class={menuClasses.separator} />
        <DropdownMenu.Item class={menuClasses.item} onSelect={handleLogout} data-testid="nav-logout-btn">
          <LogOut aria-hidden="true" />
          {$t('nav.logout')}
        </DropdownMenu.Item>
        {#if version}
          <div class="px-2 pb-1 pt-2 text-[11px] text-muted-foreground">{$t('sidebar.version', { version })}</div>
        {/if}
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
{:else}
  <!-- 会话还在加载：占住与账户按钮相同的高度，加载完成时侧边栏不跳。 -->
  <div class={cn('flex items-center gap-2.5', collapsed ? 'size-10 justify-center' : 'h-11 px-1.5')} aria-hidden="true">
    <span class="size-7 shrink-0 rounded-full skeleton-block"></span>
    {#if !collapsed}
      <span class="h-3 flex-1 rounded skeleton-block"></span>
    {/if}
  </div>
{/if}
