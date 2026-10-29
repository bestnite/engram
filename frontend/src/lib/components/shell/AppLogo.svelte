<script lang="ts">
  import { t } from '../../i18n';

  interface Props {
    /** Show only the icon (collapsed sidebar, narrow-screen top bar). */
    iconOnly?: boolean;
  }

  let { iconOnly = false }: Props = $props();
</script>

<a href="/" class="inline-flex items-center gap-2 text-[15px] font-semibold tracking-tight text-foreground">
  <!-- The project icon: the same shape as internal/web/static/icons/icon.svg (the favicon and PWA icon), three stacked
       rounded cards. It is inlined rather than loaded with <img> because the icon must follow the app's theme: icon.svg
       switches on prefers-color-scheme, while this app's theme is driven by the .dark class, so a light app theme on a
       dark system would draw the icon in the wrong colours. The three fills match icon.svg: a blue gradient in light
       mode, white with decreasing opacity in dark mode. -->
  <svg class="size-5" viewBox="0 0 512 512" aria-hidden="true">
    <rect class="fill-blue-300 dark:fill-white opacity-100 dark:opacity-50" x="184" y="140" width="192" height="140" rx="26" />
    <rect class="fill-blue-400 dark:fill-white opacity-100 dark:opacity-75" x="160" y="178" width="192" height="140" rx="26" />
    <rect class="fill-blue-600 dark:fill-white" x="136" y="216" width="192" height="140" rx="26" />
  </svg>
  <span class={iconOnly ? 'sr-only' : ''}>{$t('app.name')}</span>
</a>
