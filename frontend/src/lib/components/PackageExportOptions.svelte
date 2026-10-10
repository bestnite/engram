<script lang="ts">
  import { t } from '../i18n';
  import Checkbox from './ui/Checkbox.svelte';

  /**
   * 卡组包导出选项：单卡组导出与批量导出共用这一组复选框，
   * 两处的选项与默认值因此不会各自走样。
   * 「复习记录」只在勾选进度时出现：服务端拒绝「带复习记录却不带进度」的导出，
   * 所以取消进度时一并取消复习记录，否则先勾两项再取消进度会提交一个必然失败的请求。
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
