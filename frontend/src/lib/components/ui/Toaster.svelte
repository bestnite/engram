<script lang="ts">
  import { onMount } from 'svelte';
  import { Toaster as Sonner } from 'svelte-sonner';

  /**
   * Global toasts (svelte-sonner). Operation results (deleted, how many rows batched, failure reason) are reported as toasts,
   * instead of inserting a line of text into the page: inserted text pushes the list below it down, which is one source of "page jank".
   * The complete rules for request feedback site-wide (in progress, result, which stay in place) are in ui/toast.ts.
   *
   * Colors connect to the app tokens through the CSS variables sonner exposes, and light/dark follows app.css.
   *
   * The `theme` prop is not decorative: sonner keeps its own palette for parts the tokens above do not cover (above all
   * the close button's icon color and hover background), and it defaults to `light`. Mirroring the `.dark` class here is
   * what keeps those parts readable in dark mode — with the default, the close button drew a near-black icon on the dark
   * toast (an unreadable black blob until hover swapped the background).
   */
  let dark = $state(false);

  onMount(() => {
    // The `.dark` class on <html> is the single source of truth for the theme: the server's inline bootstrap puts it
    // there before the first frame, and both the navbar toggle and pwa.js's system-preference handler only rewrite it.
    // Observing the attribute therefore covers every path, including a system-preference change while no explicit
    // choice is stored.
    const root = document.documentElement;
    const sync = (): void => {
      dark = root.classList.contains('dark');
    };
    sync();
    const observer = new MutationObserver(sync);
    observer.observe(root, { attributes: true, attributeFilter: ['class'] });
    return () => observer.disconnect();
  });
</script>

<Sonner
  position="bottom-right"
  closeButton
  theme={dark ? 'dark' : 'light'}
  style="--normal-bg: var(--popover); --normal-text: var(--foreground); --normal-border: var(--border); --normal-bg-hover: var(--muted); --normal-border-hover: var(--border); --border-radius: 0.625rem;"
  toastOptions={{ class: 'text-[13px] shadow-lg shadow-black/8' }}
/>
