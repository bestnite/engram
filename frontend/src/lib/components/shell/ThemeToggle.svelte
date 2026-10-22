<script lang="ts">
  import { onMount } from 'svelte';
  import { Moon, Sun } from '@lucide/svelte';
  import { t } from '../../i18n';
  import { isDark, toggleTheme } from '../../theme';

  // The icon must reflect the **current** state, so read the actual value from the DOM once on mount
  // (the first frame is already set up by the inline bootstrap the server injects into .dark; this only reads, never writes, to avoid overriding the user's choice).
  let dark = $state(false);
  onMount(() => {
    dark = isDark();
  });
</script>

<!-- 刻意**不加** data-theme-toggle：pwa.js 的加载期绑定会往该属性上写 btn.onclick，
     与 Svelte 的事件监听叠加会在一次点击里切换两次（看起来没反应）。本按钮由 theme.ts 全权处理。 -->
<button
  type="button"
  data-testid="nav-theme-toggle"
  title={$t('nav.toggle_theme')}
  aria-label={$t('nav.toggle_theme')}
  onclick={() => (dark = toggleTheme())}
  class="inline-flex size-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground cursor-pointer"
>
  {#if dark}
    <Sun class="size-4" aria-hidden="true" />
  {:else}
    <Moon class="size-4" aria-hidden="true" />
  {/if}
</button>
