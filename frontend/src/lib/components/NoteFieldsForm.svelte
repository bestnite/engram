<script lang="ts">
  import { t } from '../i18n';
  import { arrayToLines, linesToArray, type FieldSpec } from '../card-fields';
  import Checkbox from './ui/Checkbox.svelte';
  import Select from './ui/Select.svelte';

  /**
   * 按题型渲染字段表单。
   *
   * 字段顺序与控件形态来自服务端的题型自描述，本组件不含任何按题型分支的
   * 业务判断；视图因此不再把 fields 当 JSON 给用户编辑。
   */
  interface Props {
    specs: FieldSpec[];
    /** 表单模型：直接就地写入（父级用 $state 持有，深响应）。 */
    fields: Record<string, unknown>;
    /** 生成 data-testid 的前缀：`<prefix>-<字段名>`。 */
    testIdPrefix?: string;
  }

  let { specs, fields, testIdPrefix = 'note-field' }: Props = $props();


  /** options 字段既用于「选项」编辑，也用于选择题答案的下标选择。 */
  const options = $derived(
    Array.isArray(fields.options) ? fields.options.map((item) => String(item)).filter((item) => item !== '') : []
  );

  function asString(key: string): string {
    const value = fields[key];
    return typeof value === 'string' ? value : value === undefined || value === null ? '' : String(value);
  }

  function asNumber(key: string): string {
    const value = fields[key];
    return typeof value === 'number' ? String(value) : '';
  }

  /** 数值输入：空串删除该键（可选数值字段的「未填」语义），否则写入数字。 */
  function setNumber(key: string, raw: string): void {
    if (raw.trim() === '') {
      delete fields[key];
      return;
    }
    const parsed = Number(raw);
    fields[key] = Number.isFinite(parsed) ? parsed : raw;
  }

  function setString(key: string, raw: string): void {
    if (raw === '') delete fields[key];
    else fields[key] = raw;
  }

  function selectedIndexes(key: string): number[] {
    const value = fields[key];
    return Array.isArray(value) ? value.map((item) => Number(item)).filter((n) => Number.isInteger(n)) : [];
  }

  function toggleIndex(key: string, index: number, checked: boolean): void {
    const current = selectedIndexes(key);
    const next = checked ? [...current, index] : current.filter((item) => item !== index);
    fields[key] = next.sort((a, b) => a - b);
  }
</script>

{#each specs as spec (spec.key)}
  <div class="block">
    {#if spec.control === 'bool'}
      <div class="flex items-center gap-2">
        <Checkbox
          testId="{testIdPrefix}-{spec.key}"
          checked={Boolean(fields[spec.key])}
          onCheckedChange={(checked) => (fields[spec.key] = checked)}
          label={$t('note.fields.' + spec.key)}
        />
        <span class="text-sm text-zinc-700 dark:text-zinc-300">{$t('note.fields.' + spec.key)}</span>
      </div>
    {:else}
      <span class="block text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
        {$t('note.fields.' + spec.key)}
      </span>
      {#if spec.control === 'textarea'}
        <textarea
          data-testid="{testIdPrefix}-{spec.key}"
          rows={spec.key === 'extra' ? 3 : 5}
          spellcheck="false"
          value={asString(spec.key)}
          oninput={(event) => setString(spec.key, event.currentTarget.value)}
          class="field-input text-sm mt-1.5 block w-full"></textarea>
      {:else if spec.control === 'lines'}
        <textarea
          data-testid="{testIdPrefix}-{spec.key}"
          rows="4"
          spellcheck="false"
          value={arrayToLines(fields[spec.key])}
          oninput={(event) => (fields[spec.key] = linesToArray(event.currentTarget.value))}
          class="field-input text-sm mt-1.5 block w-full"></textarea>
      {:else if spec.control === 'number'}
        <input
          type="text"
          inputmode="decimal"
          data-testid="{testIdPrefix}-{spec.key}"
          value={asNumber(spec.key)}
          oninput={(event) => setNumber(spec.key, event.currentTarget.value)}
          class="field-input text-sm mt-1.5 block w-full"
        />
      {:else if spec.control === 'index'}
        {#if options.length === 0}
          <p class="mt-1.5 text-xs text-zinc-400 dark:text-zinc-500">{$t('note_edit.options_first')}</p>
        {:else}
          <Select
            class="mt-1.5"
            testId="{testIdPrefix}-{spec.key}"
            ariaLabel={$t('note_edit.answer_index')}
            value={fields[spec.key] === undefined || fields[spec.key] === '' ? '' : String(fields[spec.key])}
            onValueChange={(value) => (fields[spec.key] = Number(value))}
            options={options.map((option, index) => ({ value: String(index), label: `${index + 1}. ${option}` }))}
          />
        {/if}
      {:else if spec.control === 'indexes'}
        {#if options.length === 0}
          <p class="mt-1.5 text-xs text-zinc-400 dark:text-zinc-500">{$t('note_edit.options_first')}</p>
        {:else}
          <ul class="mt-1.5 space-y-1.5">
            {#each options as option, index (index)}
              <li class="flex items-center gap-2">
                <Checkbox
                  testId="{testIdPrefix}-{spec.key}"
                  checked={selectedIndexes(spec.key).includes(index)}
                  onCheckedChange={(checked) => toggleIndex(spec.key, index, checked)}
                  label={`${index + 1}. ${option}`}
                />
                <span class="text-sm text-zinc-700 dark:text-zinc-300">{index + 1}. {option}</span>
              </li>
            {/each}
          </ul>
        {/if}
      {:else}
        <input
          type="text"
          data-testid="{testIdPrefix}-{spec.key}"
          value={asString(spec.key)}
          oninput={(event) => setString(spec.key, event.currentTarget.value)}
          class="field-input text-sm mt-1.5 block w-full"
        />
      {/if}
    {/if}
  </div>
{/each}
