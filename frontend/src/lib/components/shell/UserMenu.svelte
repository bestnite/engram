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
  import { toast } from '../ui/toast';
  import { resetSidebarDecks } from './sidebar';

  /**
   * Account menu at the bottom of the sidebar: shows the current user and collects theme, language and logout - all low-frequency actions,
   * so putting them in a menu leaves only navigation in the sidebar itself.
   */
  interface Props {
    collapsed?: boolean;
    version?: string;
  }

  let { collapsed = false, version = '' }: Props = $props();

  const user = $derived($authStore.user);
  const displayName = $derived(user ? user.display_name || user.username : '');
  const initial = $derived(displayName ? Array.from(displayName)[0]?.toUpperCase() ?? '' : '');

  // The theme menu item must reflect the **current** state, so read the actual value from the DOM once on mount
  // (the first frame is already set up by the inline bootstrap the server injects into .dark; this only reads, never writes, to avoid overriding the user's choice).
  let dark = $state(false);
  onMount(() => {
    dark = isDark();
  });

  async function handleLocale(value: string) {
    if (!(await switchLocale(value))) toast.error($t('language.save_failed'));
  }

  async function handleLogout(): Promise<void> {
    try {
      await logout();
      resetSidebarDecks();
      navigate('/login');
    } catch {
      // The error is already written into authStore; the shell decides how to present it.
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
