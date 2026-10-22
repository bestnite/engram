<script lang="ts">
  import { onMount } from 'svelte';
  import { navigate } from '../../router';

  /**
   * /admin no longer has an overview page: the old count cards only repeated the numbers from each subpage and offered no actionable information.
   * The route is kept so old links and bookmarks outside the sidebar do not break — it renders no content, it only swaps the address for
   * the first item of the admin navigation (user management).
   */
  onMount(() => {
    navigate('/admin/users', true);
  });
</script>
