import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

/**
 * Vite 构建与测试配置
 * 配置纯客户端 SPA 构建与 Vitest 测试套件。
 */
export default defineConfig({
  plugins: [
    tailwindcss(),
    svelte(),
  ],
  server: {
    port: 5173,
    strictPort: true,
  },
  build: {
    target: 'es2022',
    // 零运行时外部依赖，产物完全自闭环
    assetsInlineLimit: 4096,
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
});
