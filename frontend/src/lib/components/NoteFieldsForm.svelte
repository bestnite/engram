<script lang="ts">
  import { t } from '../i18n';
  import { arrayToLines, linesToArray, type FieldSpec } from '../card-fields';
  import Checkbox from './ui/Checkbox.svelte';
  import Select from './ui/Select.svelte';
  import HelpTip from './ui/HelpTip.svelte';

  /**
   * Renders the field form per card kind.
   *
   * Field order and control shape come from the server's card-kind self-description, and this component contains no per-kind branching
   * business logic; views therefore no longer hand fields to the user as raw JSON to edit.
   */
  interface Props {
    specs: FieldSpec[];
    /** Form model: written in place directly (the parent holds it with $state, deeply reactive). */
    fields: Record<string, unknown>;
    /** Prefix for generating data-testid: `<prefix>-<field name>`. */
    testIdPrefix?: string;
    /**
     * Reports the caret of a text control as (field key, selection start, selection end) on focus moves,
     * clicks, key presses and edits. A view that inserts at the caret keeps the last report per field; a
     * view that always appends simply omits the prop.
     */
    oncaret?: (key: string, start: number, end: number) => void;
  }

  let { specs, fields, testIdPrefix = 'note-field', oncaret }: Props = $props();

  /**
   * Fields needing syntax help → the language pack key for the help text. The help goes into a question-mark popover next to the label instead of into the label:
   * cloze syntax has several rules, and putting them in parentheses in the label stretches it into a paragraph that must be read every time you edit.
   */
  const fieldHelp: Record<string, string> = {
    text: 'note_help.text',
  };


  /** The options field is used both for editing "choices" and for picking the index of a multiple-choice answer. */
  const options = $derived(
    Array.isArray(fields.options) ? fields.options.map((item) => String(item)).filter((item) => item !== '') : []
  );

  function asString(key: string): string {
    const value = fields[key];
    return typeof value === 'string' ? value : value === undefined || value === null ? '' : String(value);
  }

  /**
   * Display value of the number box. Strings are shown as-is: they are what the user has not finished typing (e.g. "-", "1e"),
   * and stay stored as-is when setNumber cannot parse them; if this displayed as empty, the input would be cleared on the first keystroke.
   */
  function asNumber(key: string): string {
    const value = fields[key];
    if (typeof value === 'number') return String(value);
    return typeof value === 'string' ? value : '';
  }

  /** Numeric input: an empty string deletes the key (the "not filled" semantics of an optional numeric field), otherwise the number is written. */
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

  /**
   * Reports where the caret sits in one text control. Only single-string controls report: a "lines"
   * field is edited as several lines in one box, so its offsets do not map onto the stored array.
   */
  function reportCaret(
    event: Event & { currentTarget: HTMLInputElement | HTMLTextAreaElement },
    key: string
  ): void {
    if (!oncaret) return;
    const { selectionStart, selectionEnd } = event.currentTarget;
    if (selectionStart === null || selectionEnd === null) return;
    oncaret(key, selectionStart, selectionEnd);
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
  {@const helpKey = fieldHelp[spec.key]}
  <div class="block">
    {#if spec.control === 'bool'}
      <div class="flex items-center gap-2">
        <Checkbox
          testId="{testIdPrefix}-{spec.key}"
          checked={Boolean(fields[spec.key])}
          onCheckedChange={(checked) => (fields[spec.key] = checked)}
          label={$t('note.fields.' + spec.key)}
        />
        <span class="text-sm text-foreground/80">{$t('note.fields.' + spec.key)}</span>
      </div>
    {:else}
      <span class="flex items-center gap-1 text-sm font-medium text-foreground">
        {$t('note.fields.' + spec.key)}
        {#if helpKey}
          <HelpTip
            testId="{testIdPrefix}-{spec.key}-help"
            label={$t('note.fields.' + spec.key)}
            text={$t(helpKey)}
          />
        {/if}
      </span>
      {#if spec.control === 'textarea'}
        <textarea
          data-testid="{testIdPrefix}-{spec.key}"
          rows={spec.key === 'extra' ? 3 : 5}
          spellcheck="false"
          value={asString(spec.key)}
          oninput={(event) => {
            setString(spec.key, event.currentTarget.value);
            reportCaret(event, spec.key);
          }}
          onselect={(event) => reportCaret(event, spec.key)}
          onkeyup={(event) => reportCaret(event, spec.key)}
          onclick={(event) => reportCaret(event, spec.key)}
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
          <p class="mt-1.5 text-xs text-muted-foreground/70">{$t('note_edit.options_first')}</p>
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
          <p class="mt-1.5 text-xs text-muted-foreground/70">{$t('note_edit.options_first')}</p>
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
                <span class="text-sm text-foreground/80">{index + 1}. {option}</span>
              </li>
            {/each}
          </ul>
        {/if}
      {:else}
        <input
          type="text"
          data-testid="{testIdPrefix}-{spec.key}"
          value={asString(spec.key)}
          oninput={(event) => {
            setString(spec.key, event.currentTarget.value);
            reportCaret(event, spec.key);
          }}
          onselect={(event) => reportCaret(event, spec.key)}
          onkeyup={(event) => reportCaret(event, spec.key)}
          onclick={(event) => reportCaret(event, spec.key)}
          class="field-input text-sm mt-1.5 block w-full"
        />
      {/if}
    {/if}
  </div>
{/each}
