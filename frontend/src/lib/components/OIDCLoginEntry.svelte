<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../i18n';
  import { apiClient } from '../api';
  import type { OIDCInfo } from '../api';

  // OIDC 登录入口组件（DESIGN.md §4.4）。
  //
  // SSR 的登录页只在 OIDC 配置完整可用时渲染第二个登录按钮；SPA 登录视图无法在构建期知道服务端配置，
  // 因此这里在挂载时探测 GET /api/v1/auth/oidc（只读、登录前可调用），仅在 enabled 为真时渲染入口。
  // start_url 由服务端给出，与 SSR 登录页按钮的 href 同源，避免两端漂移。
  //
  // 可选 props.initial 用于服务端渲染/测试注入已知状态，避免首帧闪烁；生产由探测结果驱动。
  interface Props {
    initial?: OIDCInfo | null;
  }

  let { initial = null }: Props = $props();

  // svelte-ignore state_referenced_locally
  let info = $state<OIDCInfo | null>(initial);

  onMount(async () => {
    if (info) {
      return;
    }
    try {
      info = await apiClient.getOIDC();
    } catch {
      // 探测失败等同于「入口不可用」：绝不显示一个点了也没用的按钮。
      info = null;
    }
  });
</script>

{#if info?.enabled}
  <div class="mt-4" data-testid="oidc-entry">
    <a
      href={info.start_url}
      class="inline-flex w-full items-center justify-center rounded-xl border border-zinc-200 bg-white px-4 py-2.5 text-sm font-medium text-zinc-700 shadow-2xs hover:bg-zinc-50 hover:border-zinc-300 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-200 dark:hover:bg-zinc-700 transition-colors"
    >
      {$t('account.oidc.button')}
    </a>
  </div>
{/if}
