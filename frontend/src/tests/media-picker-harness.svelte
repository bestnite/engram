<script lang="ts">
  import MediaPicker from '../lib/components/MediaPicker.svelte';
  import type { MediaItem } from '../lib/api';

  // Test harness: the picker's open state belongs to its parent, so closing is only observable from a parent that binds it.
  let open = $state(true);
  let chosen = $state('');

  function onchoose(item: MediaItem): Promise<void> {
    chosen = item.sha256;
    return Promise.resolve();
  }
</script>

<div data-testid="harness-open">{open ? 'open' : 'closed'}</div>
<div data-testid="harness-chosen">{chosen}</div>
<MediaPicker bind:open deckId="deck-1" {onchoose} testId="picker" />
