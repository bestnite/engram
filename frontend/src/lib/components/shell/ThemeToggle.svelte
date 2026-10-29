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

<!-- Deliberately carries no data-theme-toggle attribute: internal/web/static/js/pwa.js binds btn.onclick at load time to
     every element with that attribute, and together with Svelte's listener one click would toggle the theme twice, which
     looks like nothing happened. frontend/src/lib/theme.ts alone handles this button. -->
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
