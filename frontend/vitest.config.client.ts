import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';

/**
 * Configuration dedicated to client (mount) tests.
 *
 * The default suite (the test section of vite.config.ts) runs in the node environment and renders SSR through svelte/server,
 * so neither onMount nor $effect runs, which makes it impossible to cover "auto-preview after mount / cleanup on unmount /
 * redirect after success" — behavior that only happens with a real client instance. This file separately enables happy-dom + the
 * browser resolution condition so components really mount; include takes only `*.client.ts` and leaves the default suite's global imports and include rules untouched.
 *
 * Run with: npx vitest run --config vitest.config.client.ts
 */
export default defineConfig({
  plugins: [svelte()],
  resolve: {
    // The browser condition makes svelte resolve to the client runtime (mount/onMount/$effect available).
    conditions: ['browser'],
  },
  test: {
    environment: 'happy-dom',
    include: ['src/tests/*.client.ts'],
  },
});
