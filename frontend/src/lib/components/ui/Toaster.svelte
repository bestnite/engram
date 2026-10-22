<script lang="ts">
  import { Toaster as Sonner } from 'svelte-sonner';

  /**
   * Global toasts (svelte-sonner). Operation results (deleted, how many rows batched, failure reason) are reported as toasts,
   * instead of inserting a line of text into the page: inserted text pushes the list below it down, which is one source of "page jank".
   * The complete rules for request feedback site-wide (in progress, result, which stay in place) are in ui/toast.ts.
   *
   * Colors connect to the app tokens through the CSS variables sonner exposes, and light/dark follows app.css.
   */
</script>

<Sonner
  position="bottom-right"
  closeButton
  style="--normal-bg: var(--popover); --normal-text: var(--foreground); --normal-border: var(--border); --border-radius: 0.625rem;"
  toastOptions={{ class: 'text-[13px] shadow-lg shadow-black/8' }}
/>
