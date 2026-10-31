<script lang="ts">
  import { routeStore } from '../router';
  import { RETURN_PARAM, safeReturnPath } from '../return-to';
  import { t } from '../i18n';
  import Page from '../components/ui/Page.svelte';
  import PageHeader from '../components/ui/PageHeader.svelte';
  import { guideSteps, syntaxRows } from '../note-type-syntax';

  /**
   * The note type help page: what note types, fields, templates and cards are, how the note type page is used, the full
   * template syntax, and worked examples.
   *
   * Every example is data: the field names and the template code are Anki template syntax and stay literal, while the
   * title, the explanation, the sample note and the result come from the catalog. Code is printed as text, never as HTML,
   * so an example can show markup without it being interpreted.
   */

  const deckId = $derived($routeStore.params.id || '');
  // The note type page passes its own address so a visit from the note editor keeps its way back; without one the help
  // returns to the deck's note type page.
  const back = $derived(
    safeReturnPath($routeStore.query[RETURN_PARAM]) ?? `/decks/${encodeURIComponent(deckId)}/note-types`,
  );

  interface ExampleTemplate {
    /** The template name, shown only when the example has more than one template. */
    name?: string;
    front: string;
    back: string;
  }

  interface Example {
    id: string;
    fields: string[];
    templates: ExampleTemplate[];
    css?: string;
    /** The answer control and its bindings, written as role → field. */
    control?: { name: string; bindings: Array<{ role: string; field: string }> };
  }

  const answerSeparator = '{{FrontSide}}\n\n<hr id=answer>\n\n';

  const examples: Example[] = [
    {
      id: 'basic',
      fields: ['Front', 'Back'],
      templates: [{ front: '{{Front}}', back: `${answerSeparator}{{Back}}` }],
    },
    {
      id: 'reversed',
      fields: ['Front', 'Back'],
      templates: [
        { name: 'Card 1', front: '{{Front}}', back: `${answerSeparator}{{Back}}` },
        { name: 'Card 2', front: '{{Back}}', back: `${answerSeparator}{{Front}}` },
      ],
    },
    {
      id: 'optional_reverse',
      fields: ['Front', 'Back', 'Add Reverse'],
      templates: [
        { name: 'Card 1', front: '{{Front}}', back: `${answerSeparator}{{Back}}` },
        { name: 'Card 2', front: '{{#Add Reverse}}{{Back}}{{/Add Reverse}}', back: `${answerSeparator}{{Front}}` },
      ],
    },
    {
      id: 'cloze',
      fields: ['Text', 'Back Extra'],
      templates: [
        {
          front: '{{cloze:Text}}',
          back: '{{cloze:Text}}\n\n{{#Back Extra}}\n<hr>\n{{Back Extra}}\n{{/Back Extra}}',
        },
      ],
    },
    {
      id: 'type_in',
      fields: ['Front', 'Back'],
      templates: [{ front: '{{Front}}\n\n{{type:Back}}', back: '{{Front}}\n\n<hr id=answer>\n\n{{type:Back}}' }],
    },
    {
      id: 'reading',
      fields: ['Expression', 'Meaning'],
      templates: [{ front: '{{kanji:Expression}}', back: '{{furigana:Expression}}\n\n<hr id=answer>\n\n{{Meaning}}' }],
    },
    {
      id: 'hint',
      fields: ['Front', 'Back', 'Example', 'Notes'],
      templates: [
        {
          front: '{{Front}}\n\n{{#Example}}\n<div class="example">{{hint:Example}}</div>\n{{/Example}}',
          back: `${answerSeparator}{{Back}}\n\n{{#Notes}}\n<div class="notes">{{Notes}}</div>\n{{/Notes}}\n{{^Notes}}\n<div class="notes">—</div>\n{{/Notes}}`,
        },
      ],
    },
    {
      id: 'tts',
      fields: ['Word', 'Meaning'],
      templates: [{ front: '{{Word}}\n{{tts zh_CN:Word}}', back: `${answerSeparator}{{Meaning}}` }],
    },
    {
      id: 'choice',
      fields: ['Question', 'Options', 'Answer'],
      templates: [{ front: '{{Question}}\n\n{{type:Options}}', back: '{{type:Options}}' }],
      control: {
        name: 'choice_single',
        bindings: [
          { role: 'options', field: 'Options' },
          { role: 'answer', field: 'Answer' },
        ],
      },
    },
    {
      id: 'style',
      fields: ['Front', 'Back'],
      templates: [
        {
          front: '<div class="question">{{Front}}</div>',
          back: `${answerSeparator}<div class="answer">{{Back}}</div>\n<div class="meta">{{Deck}} · {{Tags}}</div>`,
        },
      ],
      css: [
        '.card {',
        '  font-family: system-ui, sans-serif;',
        '  font-size: 22px;',
        '  text-align: center;',
        '  color: #1f2937;',
        '  background: #ffffff;',
        '}',
        '.question { font-size: 28px; font-weight: 600; }',
        '.meta { margin-top: 24px; font-size: 13px; color: #6b7280; }',
        '/* Only the cards of the second template */',
        '.card2 .question { color: #2563eb; }',
        '/* Dark theme */',
        '.card.nightMode { color: #e5e7eb; background: #111827; }',
        '.nightMode .meta { color: #9ca3af; }',
      ].join('\n'),
    },
  ];

  const sections = ['concepts', 'steps', 'syntax', 'examples'] as const;

  function scrollTo(id: string): void {
    document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }
</script>

{#snippet code(text: string, testId?: string)}
  <pre
    class="overflow-x-auto rounded-md border border-border bg-muted/50 px-3 py-2 font-mono text-xs leading-relaxed text-foreground whitespace-pre"
    data-testid={testId}><code>{text}</code></pre>
{/snippet}

<Page as="section" testId="note-type-help-view">
  <PageHeader
    title={$t('notetype_help.title')}
    back={{ href: back, label: $t('notetype_help.back'), testId: 'note-type-help-back' }}
  />

  <div class="max-w-3xl space-y-10 text-sm leading-relaxed">
    <p class="text-muted-foreground">{$t('notetype.guide.intro')}</p>

    <nav aria-label={$t('notetype_help.toc')} class="flex flex-wrap gap-2" data-testid="note-type-help-toc">
      {#each sections as section (section)}
        <button
          type="button"
          class="rounded-full border border-border px-3 py-1 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground cursor-pointer"
          onclick={() => scrollTo(`help-${section}`)}
        >
          {$t(`notetype_help.${section}.title`)}
        </button>
      {/each}
    </nav>

    <section id="help-concepts" class="scroll-mt-6 space-y-3" data-testid="note-type-help-concepts">
      <h2 class="text-lg font-semibold text-foreground">{$t('notetype_help.concepts.title')}</h2>
      <dl class="space-y-3">
        {#each ['note', 'field', 'type', 'template', 'card'] as term (term)}
          <div>
            <dt class="font-medium text-foreground">{$t(`notetype_help.concepts.${term}.term`)}</dt>
            <dd class="mt-0.5 text-muted-foreground">{$t(`notetype_help.concepts.${term}.text`)}</dd>
          </div>
        {/each}
      </dl>
      <p class="rounded-md bg-brand-soft px-3 py-2 text-foreground">{$t('notetype_help.concepts.formula')}</p>
    </section>

    <section id="help-steps" class="scroll-mt-6 space-y-3" data-testid="note-type-help-steps">
      <h2 class="text-lg font-semibold text-foreground">{$t('notetype_help.steps.title')}</h2>
      <ol class="list-decimal space-y-2 pl-5 text-muted-foreground">
        {#each guideSteps as step (step)}
          <li data-testid={`note-type-help-step-${step}`}>{$t(`notetype.guide.step.${step}`)}</li>
        {/each}
      </ol>
    </section>

    <section id="help-syntax" class="scroll-mt-6 space-y-3" data-testid="note-type-help-syntax">
      <h2 class="text-lg font-semibold text-foreground">{$t('notetype_help.syntax.title')}</h2>
      <p class="text-muted-foreground">{$t('notetype_help.syntax.intro')}</p>
      <div class="overflow-x-auto rounded-lg border border-border">
        <table class="w-full text-xs">
          <tbody>
            {#each syntaxRows as row (row.key)}
              <tr class="border-t border-border first:border-t-0">
                <td class="px-3 py-2 align-top font-mono whitespace-nowrap text-foreground">{row.code}</td>
                <td class="px-3 py-2 align-top text-muted-foreground">{$t(`notetype.syntax.${row.key}`)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>

    <section id="help-examples" class="scroll-mt-6 space-y-6" data-testid="note-type-help-examples">
      <div class="space-y-2">
        <h2 class="text-lg font-semibold text-foreground">{$t('notetype_help.examples.title')}</h2>
        <p class="text-muted-foreground">{$t('notetype_help.examples.intro')}</p>
      </div>
      {#each examples as example, index (example.id)}
        <article class="space-y-3 rounded-lg border border-border p-4" data-testid={`note-type-help-example-${example.id}`}>
          <h3 class="font-semibold text-foreground">{index + 1}. {$t(`notetype_help.example.${example.id}.title`)}</h3>
          <p class="text-muted-foreground">{$t(`notetype_help.example.${example.id}.desc`)}</p>

          <div class="text-xs">
            <span class="font-medium text-foreground">{$t('notetype_help.label.fields')}</span>
            {#each example.fields as field (field)}
              <code class="ml-1.5 rounded bg-muted px-1.5 py-0.5 font-mono text-foreground">{field}</code>
            {/each}
          </div>

          {#if example.control}
            <div class="text-xs">
              <span class="font-medium text-foreground">{$t('notetype_help.label.control')}</span>
              <span class="ml-1.5 text-muted-foreground">
                {$t(`notetype.answer.control.${example.control.name}`)} ·
                {#each example.control.bindings as binding, bindingIndex (binding.role)}
                  {bindingIndex > 0 ? ', ' : ''}{$t(`notetype.answer.role.${binding.role}`)} → <code class="font-mono text-foreground">{binding.field}</code>
                {/each}
              </span>
            </div>
          {/if}

          {#each example.templates as template, templateIndex (templateIndex)}
            <div class="space-y-2">
              {#if example.templates.length > 1}
                <div class="text-xs font-medium text-foreground">{$t('notetype_help.label.template', { name: template.name ?? '' })}</div>
              {/if}
              <div class="grid gap-2 sm:grid-cols-2">
                <div class="min-w-0 space-y-1">
                  <div class="text-xs text-muted-foreground">{$t('notetype_help.label.front')}</div>
                  {@render code(template.front)}
                </div>
                <div class="min-w-0 space-y-1">
                  <div class="text-xs text-muted-foreground">{$t('notetype_help.label.back')}</div>
                  {@render code(template.back)}
                </div>
              </div>
            </div>
          {/each}

          {#if example.css}
            <div class="space-y-1">
              <div class="text-xs text-muted-foreground">{$t('notetype_help.label.css')}</div>
              {@render code(example.css)}
            </div>
          {/if}

          <div class="space-y-1">
            <div class="text-xs text-muted-foreground">{$t('notetype_help.label.sample')}</div>
            {@render code($t(`notetype_help.example.${example.id}.sample`))}
          </div>
          <p class="rounded-md bg-muted/60 px-3 py-2 text-foreground">
            <span class="font-medium">{$t('notetype_help.label.result')}</span>
            {$t(`notetype_help.example.${example.id}.result`)}
          </p>
        </article>
      {/each}
    </section>
  </div>
</Page>
