<script lang="ts">
  import { t } from '../i18n';
  import { arrayToLines, linesToArray, type FieldSpec } from '../card-fields';
  import Checkbox from './ui/Checkbox.svelte';
  import Select from './ui/Select.svelte';
  import HelpTip from './ui/HelpTip.svelte';
  import RichTextField, { type RichFieldOptions } from './RichTextField.svelte';
  import type { MediaReference } from '../../editframe/runtime';

  /**
   * Renders the field form of one note type.
   *
   * Field order and control shape come from the specs the caller builds from the stored note type, and this component
   * contains no per-note-type branching business logic; views therefore never hand fields to the user as raw JSON to edit.
   *
   * The card face fields render as a RichTextField: a sandboxed editing frame with the formatting and
   * insertion tools in the field's own header. Every other field stays a plain control: an answer,
   * a URL or the extra note is not card face content, and a formatting button there would write
   * markup into text that is compared or linked rather than rendered.
   */
  interface Props {
    specs: FieldSpec[];
    /** Form model: written in place directly (the parent holds it with $state, deeply reactive). */
    fields: Record<string, unknown>;
    /**
     * The card face field names. Only a face field whose control is a textarea gets the rich-text editor;
     * without this prop no field gets it.
     */
    faceFields?: readonly string[];
    /** Prefix for generating data-testid: `<prefix>-<field name>`. */
    testIdPrefix?: string;
    /**
     * Presentation options per field name: sticky, collapsed, plain text, RTL, font and size.
     */
    fieldOptions?: Record<string, RichFieldOptions>;
    /**
     * Visible labels per field key. A key with no entry falls back to the machine field name.
     */
    labels?: Record<string, string>;
    /** Uploads one media file for the editor; without it the field inserts nothing by upload. */
    upload?: (file: File) => Promise<MediaReference>;
  }

  let {
    specs,
    fields,
    faceFields = [],
    testIdPrefix = 'note-field',
    fieldOptions = {},
    labels = {},
    upload,
  }: Props = $props();

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
  {@const fieldLabel = labels[spec.key] ?? $t('note.fields.' + spec.key)}
  <div class="block">
    {#if spec.control === 'bool'}
      <div class="flex items-center gap-2">
        <Checkbox
          testId="{testIdPrefix}-{spec.key}"
          checked={Boolean(fields[spec.key])}
          onCheckedChange={(checked) => (fields[spec.key] = checked)}
          label={fieldLabel}
        />
        <span class="text-sm text-foreground/80">{fieldLabel}</span>
      </div>
    {:else}
      <div class="flex items-center gap-1">
        <span class="min-w-0 text-sm font-medium text-foreground">{fieldLabel}</span>
        {#if helpKey}
          <HelpTip
            testId="{testIdPrefix}-{spec.key}-help"
            label={fieldLabel}
            text={$t(helpKey)}
          />
        {/if}
      </div>
      {#if spec.control === 'textarea' && faceFields.includes(spec.key)}
        <div class="mt-1.5">
          <RichTextField
            value={asString(spec.key)}
            label={fieldLabel}
            testId="{testIdPrefix}-{spec.key}"
            options={fieldOptions[spec.key]}
            {upload}
            onvalue={(value) => setString(spec.key, value)}
          />
        </div>
      {:else if spec.control === 'textarea'}
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
          oninput={(event) => setString(spec.key, event.currentTarget.value)}
          class="field-input text-sm mt-1.5 block w-full"
        />
      {/if}
    {/if}
  </div>
{/each}
