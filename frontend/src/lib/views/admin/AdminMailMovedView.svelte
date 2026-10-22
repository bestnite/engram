<script lang="ts">
  import { onMount } from 'svelte';
  import { navigate } from '../../router';

  /**
   * Mail templates have been merged into the "mail" page (/admin/smtp): right after configuring the sending channel comes "what the outgoing mail looks like",
   * and splitting them into two tabs only makes people click back and forth.
   *
   * This route exists only so old links and bookmarks do not break — it renders no content, it just swaps the address over, so what the browser shows
   * is that page rather than a copy.
   */
  onMount(() => {
    navigate('/admin/smtp', true);
  });
</script>
