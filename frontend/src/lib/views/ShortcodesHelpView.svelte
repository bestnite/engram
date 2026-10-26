<script lang="ts">
  import { Copy } from '@lucide/svelte';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import SettingsSection from '../components/ui/SettingsSection.svelte';
  import Button from '../components/ui/Button.svelte';
  import { toast } from '../components/ui/toast';
  import { t } from '../i18n';

  /**
   * Reference page for the three card-content shortcodes.
   *
   * The grammar described here is the one the server's parser implements (`internal/render/shortcode.go`): one line
   * `{{name:body|key=value|...}}` ending at the first `}}`, exactly the names tts, audio and ruby, and a malformed
   * shortcode emitted as its source text instead of an error.
   *
   * Every example is shown as literal text, never rendered: rendering it here would demonstrate the expansion instead
   * of the syntax, and the reader could not copy it. For the same reason the examples are constants rather than
   * catalog entries — code is not translated, only the explanation around it is.
   */

  interface ParamRow {
    /** The literal parameter key, as written in the shortcode. */
    key: string;
    /** Catalog key of the one-sentence description. */
    description: string;
    optional: boolean;
  }

  const FORM = '{{name:body|key=value|...}}';
  const TTS_EXAMPLE = '{{tts:こんにちは|lang=ja-JP|rate=0.9}}';
  const AUDIO_EXAMPLE = '{{audio:/media/<sha256>|start=12.5|end=15|rate=0.75|loop}}';
  const RUBY_EXAMPLE = '{{ruby:漢字|かんじ}}';

  const TTS_PARAMS: ParamRow[] = [
    { key: 'lang', description: 'help.shortcode.tts.lang', optional: true },
    { key: 'rate', description: 'help.shortcode.tts.rate', optional: true },
  ];

  const AUDIO_PARAMS: ParamRow[] = [
    { key: 'start', description: 'help.shortcode.audio.start', optional: true },
    { key: 'end', description: 'help.shortcode.audio.end', optional: true },
    { key: 'rate', description: 'help.shortcode.audio.rate', optional: true },
    { key: 'loop', description: 'help.shortcode.audio.loop', optional: true },
  ];

  /** Every rule in the same order on every screen size; each entry is one catalog key. */
  const RULES = [
    'help.shortcode.rule.fallback',
    'help.shortcode.rule.code',
    'help.shortcode.rule.text_kept',
    'help.shortcode.rule.options',
    'help.shortcode.rule.mail',
    'help.shortcode.rule.metadata',
    'help.shortcode.rule.no_more',
  ];

  /**
   * Copies one example verbatim.
   *
   * The clipboard API is absent outside a secure context and in some embedded browsers, so it is checked before use.
   * There is no fallback copy path; when copying fails the toast says so instead of reporting a success that did not
   * happen.
   */
  async function copyExample(code: string): Promise<void> {
    if (typeof navigator === 'undefined' || !navigator.clipboard?.writeText) {
      toast.error($t('help.shortcode.copy_failed'));
      return;
    }
    try {
      await navigator.clipboard.writeText(code);
      toast.success($t('help.shortcode.copied'));
    } catch {
      toast.error($t('help.shortcode.copy_failed'));
    }
  }
</script>

{#snippet example(code: string, name: string)}
  <div class="overflow-hidden rounded-lg border border-border bg-surface" data-testid={`shortcode-example-${name}`}>
    <div class="flex items-center justify-between gap-3 border-b border-border py-1 pl-3 pr-1.5">
      <span class="text-[11px] text-muted-foreground">{$t('help.shortcode.example')}</span>
      <Button
        variant="ghost"
        size="xs"
        label={$t('help.shortcode.copy', { name })}
        title={$t('help.shortcode.copy', { name })}
        testId={`shortcode-copy-${name}`}
        onclick={() => copyExample(code)}
      >
        <Copy class="size-3.5" aria-hidden="true" />
      </Button>
    </div>
    <!-- Whitespace inside pre matters, so the code element stays on one line with its parent. -->
    <pre class="overflow-x-auto px-3 py-2.5 font-mono text-[13px] leading-relaxed text-foreground"><code>{code}</code></pre>
  </div>
{/snippet}

{#snippet paramRow(param: ParamRow)}
  <div class="grid gap-x-4 gap-y-1 px-3 py-2 sm:grid-cols-[5.5rem_minmax(0,1fr)] sm:items-baseline">
    <dt class="flex flex-wrap items-baseline gap-x-1.5 font-mono text-[13px] text-foreground">
      {param.key}
      {#if param.optional}
        <span class="font-sans text-[11px] text-muted-foreground/70">{$t('help.shortcode.param.optional')}</span>
      {/if}
    </dt>
    <dd class="text-[13px] leading-relaxed text-muted-foreground">{$t(param.description)}</dd>
  </div>
{/snippet}

{#snippet paramList(params: ParamRow[])}
  <dl class="mt-3 divide-y divide-border rounded-lg border border-border">
    {#each params as param (param.key)}
      {@render paramRow(param)}
    {/each}
  </dl>
{/snippet}

<Page testId="shortcodes-help">
  <PageHeader
    title={$t('help.shortcode.title')}
    description={$t('help.shortcode.intro')}
    testId="shortcodes-help-title"
  />

  <SettingsSection
    title={$t('help.shortcode.grammar.title')}
    description={$t('help.shortcode.grammar.intro')}
    testId="shortcode-grammar"
  >
    <pre data-testid="shortcode-form" class="overflow-x-auto rounded-lg border border-border bg-surface px-3 py-2.5 font-mono text-[13px] leading-relaxed text-foreground"><code>{FORM}</code></pre>
    <ul class="mt-4 list-disc space-y-1.5 pl-4 text-[13px] leading-relaxed text-muted-foreground marker:text-muted-foreground/40">
      <li>{$t('help.shortcode.grammar.line')}</li>
      <li>{$t('help.shortcode.grammar.names')}</li>
      <li>{$t('help.shortcode.grammar.params')}</li>
    </ul>
  </SettingsSection>

  <section
    class="grid gap-x-10 gap-y-4 border-t border-border py-8 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
    aria-labelledby="shortcode-tts"
    data-testid="shortcode-tts"
  >
    <div>
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span class="font-mono text-[11px] tabular-nums text-muted-foreground/60" aria-hidden="true">01</span>
        <h2 id="shortcode-tts" class="text-sm font-semibold text-foreground">{$t('help.shortcode.tts.title')}</h2>
        <code class="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">tts</code>
      </div>
      <p class="mt-1.5 text-[13px] leading-relaxed text-pretty text-muted-foreground">{$t('help.shortcode.tts.summary')}</p>
      <p class="mt-2 text-[13px] leading-relaxed text-pretty text-foreground/75">{$t('help.shortcode.tts.play')}</p>
    </div>
    <div class="min-w-0">
      {@render example(TTS_EXAMPLE, 'tts')}
      {@render paramList(TTS_PARAMS)}
    </div>
  </section>

  <section
    class="grid gap-x-10 gap-y-4 border-t border-border py-8 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
    aria-labelledby="shortcode-audio"
    data-testid="shortcode-audio"
  >
    <div>
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span class="font-mono text-[11px] tabular-nums text-muted-foreground/60" aria-hidden="true">02</span>
        <h2 id="shortcode-audio" class="text-sm font-semibold text-foreground">{$t('help.shortcode.audio.title')}</h2>
        <code class="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">audio</code>
      </div>
      <p class="mt-1.5 text-[13px] leading-relaxed text-pretty text-muted-foreground">{$t('help.shortcode.audio.summary')}</p>
      <p class="mt-2 text-[13px] leading-relaxed text-pretty text-foreground/75">{$t('help.shortcode.audio.play')}</p>
    </div>
    <div class="min-w-0">
      {@render example(AUDIO_EXAMPLE, 'audio')}
      <p
        data-testid="shortcode-audio-src"
        class="mt-3 rounded-md bg-muted/60 px-3 py-2 text-xs leading-relaxed text-muted-foreground"
      >
        {$t('help.shortcode.audio.src')}
      </p>
      {@render paramList(AUDIO_PARAMS)}
    </div>
  </section>

  <section
    class="grid gap-x-10 gap-y-4 border-t border-border py-8 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
    aria-labelledby="shortcode-ruby"
    data-testid="shortcode-ruby"
  >
    <div>
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span class="font-mono text-[11px] tabular-nums text-muted-foreground/60" aria-hidden="true">03</span>
        <h2 id="shortcode-ruby" class="text-sm font-semibold text-foreground">{$t('help.shortcode.ruby.title')}</h2>
        <code class="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">ruby</code>
      </div>
      <p class="mt-1.5 text-[13px] leading-relaxed text-pretty text-muted-foreground">{$t('help.shortcode.ruby.summary')}</p>
    </div>
    <div class="min-w-0">
      {@render example(RUBY_EXAMPLE, 'ruby')}
      <ul class="mt-3 list-disc space-y-1.5 pl-4 text-[13px] leading-relaxed text-muted-foreground marker:text-muted-foreground/40">
        <li>{$t('help.shortcode.ruby.form')}</li>
        <li>{$t('help.shortcode.ruby.chars')}</li>
      </ul>
    </div>
  </section>

  <SettingsSection
    title={$t('help.shortcode.rules.title')}
    description={$t('help.shortcode.rules.intro')}
    testId="shortcode-rules"
  >
    <ul class="list-disc space-y-2 pl-4 text-[13px] leading-relaxed text-muted-foreground marker:text-muted-foreground/40">
      {#each RULES as key (key)}
        <li>{$t(key)}</li>
      {/each}
    </ul>
  </SettingsSection>

  <SettingsSection title={$t('help.shortcode.where.title')} testId="shortcode-where">
    <p class="text-[13px] leading-relaxed text-muted-foreground">{$t('help.shortcode.where.body')}</p>
  </SettingsSection>
</Page>
