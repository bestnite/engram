<script lang="ts">
  import { t } from '../i18n';
  import Checkbox from './ui/Checkbox.svelte';

  /**
   * Package export options: single-deck export and bulk export share this set of checkboxes,
   * so the options and defaults cannot drift apart in the two places.
   * "Review history" appears only when progress is checked: the server rejects an export that has review history but no progress,
   * so unchecking progress also unchecks review history, otherwise checking both and then unchecking progress would submit a request bound to fail.
   */
  interface Props {
    includeMedia?: boolean;
    includeProgress?: boolean;
    includeReviews?: boolean;
    includeWeights?: boolean;
  }

  let {
    includeMedia = $bindable(true),
    includeProgress = $bindable(false),
    includeReviews = $bindable(false),
    includeWeights = $bindable(false),
  }: Props = $props();
</script>

<div class="space-y-3 text-sm text-foreground">
  <label class="flex items-center gap-2 cursor-pointer">
    <Checkbox bind:checked={includeMedia} label={$t('package.export.include_media')} testId="export-include-media" />
    <span>{$t('package.export.include_media')}</span>
  </label>
  <label class="flex items-center gap-2 cursor-pointer">
    <Checkbox
      bind:checked={includeProgress}
      label={$t('package.export.include_progress')}
      testId="export-include-progress"
      onCheckedChange={(checked) => { if (!checked) includeReviews = false; }}
    />
    <span>{$t('package.export.include_progress')}</span>
  </label>
  {#if includeProgress}
    <label class="flex items-center gap-2 pl-6 cursor-pointer">
      <Checkbox bind:checked={includeReviews} label={$t('package.export.include_reviews')} testId="export-include-reviews" />
      <span>{$t('package.export.include_reviews')}</span>
    </label>
  {/if}
  <label class="flex items-center gap-2 cursor-pointer">
    <Checkbox bind:checked={includeWeights} label={$t('package.export.include_weights')} testId="export-include-weights" />
    <span>{$t('package.export.include_weights')}</span>
  </label>
</div>
