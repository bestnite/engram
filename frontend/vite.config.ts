import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

/**
 * Vite build and test configuration
 * Configures the pure client-side SPA build and the Vitest test suite.
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
    // No external runtime dependencies; the output is fully self-contained
    assetsInlineLimit: 4096,
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
});
