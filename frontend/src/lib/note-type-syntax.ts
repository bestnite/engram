/**
 * The template syntax reference and the note type guide steps, shared by the note type editor's quick reference and the
 * note type help page so the two never list different syntax.
 *
 * The code column is Anki template syntax, written literally and never translated; each description comes from the
 * catalog under `notetype.syntax.<key>`. The list covers what the renderer supports (internal/notetype).
 */
export const syntaxRows: ReadonlyArray<{ code: string; key: string }> = [
  { code: '{{Front}}', key: 'field' },
  { code: '{{#Extra}} … {{/Extra}}', key: 'if' },
  { code: '{{^Extra}} … {{/Extra}}', key: 'unless' },
  { code: '{{hint:Extra}}', key: 'hint' },
  { code: '{{type:Back}}', key: 'type' },
  { code: '{{cloze:Text}}', key: 'cloze' },
  { code: '{{furigana:Expression}}', key: 'furigana' },
  { code: '{{tts zh_CN:Text}}', key: 'tts' },
  { code: '{{text:Front}}', key: 'text' },
  { code: '{{Tags}} {{Deck}} {{Subdeck}} {{Type}} {{Card}}', key: 'special' },
  { code: '{{FrontSide}}', key: 'frontside' },
];

/** The steps of working with note types, in order; each text is the catalog key `notetype.guide.step.<step>`. */
export const guideSteps: ReadonlyArray<string> = ['create', 'fields', 'templates', 'style', 'preview', 'use', 'tools'];
