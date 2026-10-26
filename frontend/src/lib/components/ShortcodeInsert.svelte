<script lang="ts">
  import { t, localeStore } from '../i18n';
  import {
    buildShortcode,
    type ShortcodeInput,
    type ShortcodeIssueCode,
    type ShortcodeName,
  } from '../shortcode-source';
  import Button from './ui/Button.svelte';
  import Checkbox from './ui/Checkbox.svelte';
  import SegmentedControl from './ui/SegmentedControl.svelte';
  import MediaPicker from './MediaPicker.svelte';

  /**
   * Insertion panel for the three shortcodes the renderer expands.
   *
   * It writes source text only — never rendered HTML — into the field the editor's own "insert into
   * field" select points at, so the panel carries no second field selector. The exact source is
   * shown before insertion, and insertion is unavailable while the current input would be printed
   * verbatim by the server instead of expanding.
   */
  interface Props {
    /** Localized label of the field the insertion targets, shown so the target is unambiguous. */
    targetLabel: string;
    /** False when the chosen field holds no single string (a list field), which cannot take a shortcode. */
    targetWritable: boolean;
    /** Receives the finished source text; the view owns caret handling and writes the field. */
    oninsert: (source: string) => void;
  }

  let { targetLabel, targetWritable, oninsert }: Props = $props();

  const uid = $props.id();

  let name = $state<ShortcodeName>('tts');

  let ttsBody = $state('');
  let ttsLang = $state('');
  let ttsRate = $state('');

  let audioSource = $state('');
  let audioStart = $state('');
  let audioEnd = $state('');
  let audioRate = $state('');
  let audioLoop = $state(false);

  let rubyBase = $state('');
  let rubyReading = $state('');

  // One message per rejection reason: a code is never shown to the user, and no regular expression
  // is either — the sentence says what to do instead.
  const ISSUE_KEYS: Record<ShortcodeIssueCode, string> = {
    body_required: 'shortcode.insert.error.body_required',
    body_single_line: 'shortcode.insert.error.body_single_line',
    body_nested: 'shortcode.insert.error.body_nested',
    body_closing: 'shortcode.insert.error.body_closing',
    body_separator: 'shortcode.insert.error.body_separator',
    body_control: 'shortcode.insert.error.body_control',
    lang_shape: 'shortcode.insert.error.lang_shape',
    rate_shape: 'shortcode.insert.error.rate_shape',
    media_required: 'shortcode.insert.error.media_required',
    media_shape: 'shortcode.insert.error.media_shape',
    time_shape: 'shortcode.insert.error.time_shape',
    range_order: 'shortcode.insert.error.range_order',
    ruby_base_required: 'shortcode.insert.error.ruby_base_required',
    ruby_reading_required: 'shortcode.insert.error.ruby_reading_required',
    ruby_part: 'shortcode.insert.error.ruby_part',
  };

  const tabs = $derived([
    { value: 'tts', label: $t('shortcode.insert.tab.tts') },
    { value: 'audio', label: $t('shortcode.insert.tab.audio') },
    { value: 'ruby', label: $t('shortcode.insert.tab.ruby') },
  ]);

  const input = $derived<ShortcodeInput>(
    name === 'tts'
      ? { name: 'tts', body: ttsBody, lang: ttsLang, rate: ttsRate }
      : name === 'audio'
        ? { name: 'audio', source: audioSource, start: audioStart, end: audioEnd, rate: audioRate, loop: audioLoop }
        : { name: 'ruby', base: rubyBase, reading: rubyReading }
  );

  const result = $derived(buildShortcode(input));

  // One reason at a time: an unwritable target outranks the input error, because changing the input
  // would not make this insertion possible.
  const errorKey = $derived(
    !targetWritable
      ? 'shortcode.insert.error.not_text_field'
      : result.ok
        ? ''
        : ISSUE_KEYS[result.issue.code]
  );
  // The marked input is only the one the shown reason is about; a reason about the target field marks
  // no input at all.
  const invalidField = $derived(!targetWritable || result.ok ? '' : result.issue.field);
  const canInsert = $derived(result.ok && targetWritable);
  const preview = $derived(result.ok ? result.source : '');

  // Nothing typed in the current section yet: the reason is then written as guidance, not as an
  // error, so a panel that was just opened does not greet the user with a red complaint.
  const pristine = $derived(
    name === 'tts'
      ? ttsBody === '' && ttsLang === '' && ttsRate === ''
      : name === 'audio'
        ? audioSource === '' && audioStart === '' && audioEnd === '' && audioRate === ''
        : rubyBase === '' && rubyReading === ''
  );

  /** Marks the one input the current rejection is about, so the message and the field are linked. */
  function describedBy(field: string): string | undefined {
    return invalidField === field ? `${uid}-error` : undefined;
  }

  function submit(): void {
    if (!result.ok || !targetWritable) return;
    oninsert(result.source);
  }
</script>

<section
  data-testid="shortcode-insert"
  aria-labelledby="{uid}-heading"
  class="space-y-4 rounded-xl border border-input p-4"
>
  <div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
    <h2 id="{uid}-heading" class="text-lg font-semibold">{$t('shortcode.insert.heading')}</h2>
    <a
      href="/help/shortcodes"
      data-testid="shortcode-insert-help"
      class="rounded-sm text-sm text-brand underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
    >
      {$t('shortcode.insert.help')}
    </a>
  </div>

  <p class="text-xs text-muted-foreground">{$t('shortcode.insert.target', { field: targetLabel })}</p>

  <SegmentedControl
    value={name}
    options={tabs}
    ariaLabel={$t('shortcode.insert.heading')}
    testId="shortcode-insert-tabs"
    itemTestIdPrefix="shortcode-tab-"
    onValueChange={(value) => (name = value as ShortcodeName)}
  />

  {#if name === 'tts'}
    <div class="space-y-3">
      <div>
        <label for="{uid}-tts-body" class="block text-sm font-medium text-foreground">
          {$t('shortcode.insert.tts.body')}
        </label>
        <input
          id="{uid}-tts-body"
          data-testid="shortcode-tts-body"
          bind:value={ttsBody}
          placeholder={$t('shortcode.insert.tts.body_placeholder')}
          aria-invalid={invalidField === 'body'}
          aria-describedby={describedBy('body')}
          class="field-input mt-1.5 w-full text-sm"
        />
      </div>
      <div class="grid grid-cols-2 gap-3">
        <div class="min-w-0">
          <label for="{uid}-tts-lang" class="block text-sm font-medium text-foreground">
            {$t('shortcode.insert.tts.lang')}
          </label>
          <!-- The instance locale is a real tag of the right shape, and a better default than a fixed example. -->
          <input
            id="{uid}-tts-lang"
            data-testid="shortcode-tts-lang"
            bind:value={ttsLang}
            placeholder={$localeStore}
            spellcheck="false"
            aria-invalid={invalidField === 'lang'}
            aria-describedby={describedBy('lang')}
            class="field-input mt-1.5 w-full text-sm"
          />
        </div>
        <div class="min-w-0">
          <label for="{uid}-tts-rate" class="block text-sm font-medium text-foreground">
            {$t('shortcode.insert.tts.rate')}
          </label>
          <input
            id="{uid}-tts-rate"
            data-testid="shortcode-tts-rate"
            bind:value={ttsRate}
            placeholder="0.9"
            inputmode="decimal"
            spellcheck="false"
            aria-invalid={invalidField === 'rate'}
            aria-describedby={describedBy('rate')}
            class="field-input mt-1.5 w-full text-sm"
          />
        </div>
      </div>
    </div>
  {:else if name === 'audio'}
    <div class="space-y-3">
      <div>
        <label for="{uid}-audio-source" class="block text-sm font-medium text-foreground">
          {$t('shortcode.insert.audio.source')}
        </label>
        <input
          id="{uid}-audio-source"
          data-testid="shortcode-audio-source"
          bind:value={audioSource}
          placeholder="/media/…"
          spellcheck="false"
          aria-invalid={invalidField === 'source'}
          aria-describedby={describedBy('source')}
          class="field-input mt-1.5 w-full font-mono text-xs"
        />
        <p class="mt-1.5 text-xs text-muted-foreground">{$t('shortcode.insert.audio.source_hint')}</p>
        <!-- Only audio items are listed, so picking one can only ever produce an audio shortcode. -->
        <div class="mt-2">
          <MediaPicker
            mimePrefix="audio/"
            idPrefix="shortcode-audio"
            size="sm"
            openLabel={$t('shortcode.insert.audio.library')}
            emptyLabel={$t('shortcode.insert.audio.empty')}
            onselect={(item) => (audioSource = item.url)}
          />
        </div>
      </div>
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-3">
        <div class="min-w-0">
          <label for="{uid}-audio-start" class="block text-sm font-medium text-foreground">
            {$t('shortcode.insert.audio.start')}
          </label>
          <input
            id="{uid}-audio-start"
            data-testid="shortcode-audio-start"
            bind:value={audioStart}
            placeholder="0"
            inputmode="decimal"
            spellcheck="false"
            aria-invalid={invalidField === 'start'}
            aria-describedby={describedBy('start')}
            class="field-input mt-1.5 w-full text-sm"
          />
        </div>
        <div class="min-w-0">
          <label for="{uid}-audio-end" class="block text-sm font-medium text-foreground">
            {$t('shortcode.insert.audio.end')}
          </label>
          <input
            id="{uid}-audio-end"
            data-testid="shortcode-audio-end"
            bind:value={audioEnd}
            placeholder="15"
            inputmode="decimal"
            spellcheck="false"
            aria-invalid={invalidField === 'end'}
            aria-describedby={describedBy('end')}
            class="field-input mt-1.5 w-full text-sm"
          />
        </div>
        <div class="min-w-0">
          <label for="{uid}-audio-rate" class="block text-sm font-medium text-foreground">
            {$t('shortcode.insert.audio.rate')}
          </label>
          <input
            id="{uid}-audio-rate"
            data-testid="shortcode-audio-rate"
            bind:value={audioRate}
            placeholder="1"
            inputmode="decimal"
            spellcheck="false"
            aria-invalid={invalidField === 'rate'}
            aria-describedby={describedBy('rate')}
            class="field-input mt-1.5 w-full text-sm"
          />
        </div>
      </div>
      <div class="flex items-center gap-2">
        <Checkbox
          testId="shortcode-audio-loop"
          checked={audioLoop}
          onCheckedChange={(checked) => (audioLoop = checked)}
          label={$t('shortcode.insert.audio.loop')}
        />
        <span class="text-sm text-foreground/80">{$t('shortcode.insert.audio.loop')}</span>
      </div>
    </div>
  {:else}
    <div class="grid grid-cols-2 gap-3">
      <div class="min-w-0">
        <label for="{uid}-ruby-base" class="block text-sm font-medium text-foreground">
          {$t('shortcode.insert.ruby.base')}
        </label>
        <input
          id="{uid}-ruby-base"
          data-testid="shortcode-ruby-base"
          bind:value={rubyBase}
          aria-invalid={invalidField === 'base'}
          aria-describedby={describedBy('base')}
          class="field-input mt-1.5 w-full text-sm"
        />
      </div>
      <div class="min-w-0">
        <label for="{uid}-ruby-reading" class="block text-sm font-medium text-foreground">
          {$t('shortcode.insert.ruby.reading')}
        </label>
        <input
          id="{uid}-ruby-reading"
          data-testid="shortcode-ruby-reading"
          bind:value={rubyReading}
          aria-invalid={invalidField === 'reading'}
          aria-describedby={describedBy('reading')}
          class="field-input mt-1.5 w-full text-sm"
        />
      </div>
    </div>
  {/if}

  <div class="space-y-2 border-t border-border pt-3">
    <span class="block text-sm font-medium text-foreground">{$t('shortcode.insert.preview')}</span>
    <!-- Source text, never rendered: the panel must not create an HTML sink, and the user needs to
         see exactly what will be written. An empty block is held open so the button never jumps. -->
    <code
      data-testid="shortcode-insert-preview"
      class="block min-h-9 overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-xs break-all whitespace-pre-wrap text-foreground {preview
        ? ''
        : 'opacity-60'}"
    >{preview || '—'}</code>
    <div aria-live="polite">
      {#if errorKey}
        <p
          id="{uid}-error"
          data-testid="shortcode-insert-error"
          class="text-xs {pristine && targetWritable ? 'text-muted-foreground' : 'text-destructive-foreground'}"
        >
          {$t(errorKey)}
        </p>
      {/if}
    </div>
    <Button testId="shortcode-insert-submit" disabled={!canInsert} onclick={submit}>
      {$t('shortcode.insert.action')}
    </Button>
  </div>
</section>
