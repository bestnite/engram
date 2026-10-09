import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';

/**
 * 客户端（挂载）测试专用配置。
 *
 * 默认套件（vite.config.ts 的 test 段）跑在 node 环境、用 svelte/server 做 SSR 渲染，
 * onMount 与 $effect 都不执行，因此无法覆盖「挂载后自动预览 / 卸载清理 / 成功后跳转」
 * 这类只有真实客户端实例才发生的行为。这里单独开 happy-dom + browser 解析条件，让组件
 * 真正挂载；include 只收 `*.client.ts`，不改动默认套件的全局导入与 include 规则。
 *
 * 运行：npx vitest run --config vitest.config.client.ts
 */
export default defineConfig({
  plugins: [svelte()],
  resolve: {
    // browser 条件让 svelte 解析到客户端运行时（mount/onMount/$effect 可用）。
    conditions: ['browser'],
  },
  test: {
    environment: 'happy-dom',
    include: ['src/tests/*.client.ts'],
  },
});
