<script lang="ts">
  import { onMount } from 'svelte';
  import { Moon, Sun } from '@lucide/svelte';
  import { t } from '../../i18n';
  import { isDark, toggleTheme } from '../../theme';

  // 图标要反映**当前**状态，所以挂载时从 DOM 读一次实际值
  // （首帧由服务端注入的内联引导设好 .dark，这里只读不改，避免覆盖用户选择）。
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
