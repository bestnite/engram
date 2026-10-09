<script lang="ts">
  import { t } from '../../i18n';
  import Button from './Button.svelte';

  /**
   * 表格下方的翻页条：右对齐的「页码 / 上一页 / 下一页」。
   * 管理面板几张表各写过一份，样式与禁用逻辑各不相同，这里收成一处。
   */
  interface Props {
    page: number;
    pages: number;
    onPage: (page: number) => void;
    /** 生成 `<prefix>-pager`、`<prefix>-prev`、`<prefix>-next` 三个 data-testid。 */
    testIdPrefix: string;
  }

  let { page, pages, onPage, testIdPrefix }: Props = $props();
</script>

<div class="mt-3 flex items-center justify-end gap-2 text-[13px] text-muted-foreground" data-testid="{testIdPrefix}-pager">
  <span class="tabular-nums">{page} / {pages}</span>
  <Button testId="{testIdPrefix}-prev" variant="outline" size="sm" disabled={page <= 1} onclick={() => onPage(page - 1)}>{$t('admin.common.prev')}</Button>
  <Button testId="{testIdPrefix}-next" variant="outline" size="sm" disabled={page >= pages} onclick={() => onPage(page + 1)}>{$t('admin.common.next')}</Button>
</div>
