import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import NoteFieldsForm from '../lib/components/NoteFieldsForm.svelte';
import { emptyFields } from '../lib/card-fields';
import { createCatalog, fieldsForKind } from '../lib/card-types';
import { setLocale } from '../lib/i18n';
import { CARD_TYPES } from './card-type-fixture';

/**
 * 挖空语法说明放在标签旁的问号浮层里：标签本身只剩字段名，
 * 没有语法要讲的字段不出现问号按钮。
 */
describe('NoteFieldsForm field help', () => {
  const catalog = createCatalog(CARD_TYPES.kinds);

  beforeEach(() => {
    setLocale('zh-CN');
  });

  function renderKind(kind: string): string {
    const specs = fieldsForKind(catalog, kind);
    return render(NoteFieldsForm, { props: { specs, fields: emptyFields(specs) } }).html;
  }

  it('keeps the cloze label short and moves the syntax into a help button', () => {
    const html = renderKind('cloze');
    expect(html).toContain('data-testid="note-field-text-help"');
    expect(html).toContain('aria-label="文本是什么意思？"');
    // 语法说明在浮层打开前不出现在页面上，标签里也不再带它。
    expect(html).not.toContain('{{c1::');
  });

  it('adds no help button to fields without syntax to explain', () => {
    expect(renderKind('basic')).not.toContain('-help"');
  });
});
