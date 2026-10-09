<script lang="ts">
  import { t } from '../../i18n';

  interface Props {
    /** 只显示图标（收起的侧边栏、窄屏顶栏）。 */
    iconOnly?: boolean;
  }

  let { iconOnly = false }: Props = $props();
</script>

<a href="/" class="inline-flex items-center gap-2 text-[15px] font-semibold tracking-tight text-foreground">
  <!-- 项目图标：与 static/icons/icon.svg（favicon/PWA 图标）同一形状——三张叠放的圆角卡。
       这里内联而不是 <img>：图标要随应用主题变色，而 icon.svg 内部用的是
       prefers-color-scheme，与本应用由 .dark 类驱动的主题不同步（浅色模式 + 深色系统会瞎）。
       三档填充与 icon.svg 一致：浅色蓝渐变 / 深色白 + 递减不透明度。 -->
  <svg class="size-5" viewBox="0 0 512 512" aria-hidden="true">
    <rect class="fill-blue-300 dark:fill-white opacity-100 dark:opacity-50" x="184" y="140" width="192" height="140" rx="26" />
    <rect class="fill-blue-400 dark:fill-white opacity-100 dark:opacity-75" x="160" y="178" width="192" height="140" rx="26" />
    <rect class="fill-blue-600 dark:fill-white" x="136" y="216" width="192" height="140" rx="26" />
  </svg>
  <span class={iconOnly ? 'sr-only' : ''}>{$t('app.name')}</span>
</a>
