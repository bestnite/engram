import { defineConfig } from 'vitest/config';
import { fileURLToPath } from 'node:url';
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
    rollupOptions: {
      // Two independent entries: the SPA shell (index.html) and the review iframe document (card-frame.html).
      // They must be listed explicitly because Vite otherwise builds index.html alone; the SPA output keeps its
      // index.html name so the Go shell keeps serving the same entry.
      input: {
        main: fileURLToPath(new URL('./index.html', import.meta.url)),
        cardFrame: fileURLToPath(new URL('./card-frame.html', import.meta.url)),
      },
    },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
});
