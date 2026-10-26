import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import NoteFieldsForm from '../lib/components/NoteFieldsForm.svelte';
import { emptyFields } from '../lib/card-fields';
import { createCatalog, fieldsForKind } from '../lib/card-types';
import { setLocale } from '../lib/i18n';
import { CARD_TYPES } from './card-type-fixture';

/**
 * Formatting is a source edit, not a rich-text mode: these cases mount the field form in a real
 * browser-like environment and check the part only a mounted component can show — that a toolbar
 * press or a shortcut rewrites the text control value, leaves the caret where a writer expects,
 * keeps focus in the field, and never runs for a field that stores an array.
 */

const catalog = createCatalog(CARD_TYPES.kinds);
let target: HTMLElement;
let fields: Record<string, unknown>;
let instance: ReturnType<typeof mount> | null = null;

function mountKind(
  kind: string,
  values: Record<string, unknown> = {},
  props: Record<string, unknown> = {}
): void {
  const specs = fieldsForKind(catalog, kind);
  fields = emptyFields(specs);
  for (const [key, value] of Object.entries(values)) fields[key] = value;
  target = document.createElement('div');
  document.body.appendChild(target);
  instance = mount(NoteFieldsForm, { target, props: { specs, fields, ...props } });
  // Binding a control to the component happens in mount's effect flush; a real user cannot click
  // before that flush has run, so the cases apply the same boundary instead of clicking too early.
  flushSync();
}

/** Flush microtasks and apply Svelte's pending state synchronously, so the assertions see the DOM after the tick in applyFormat. */
async function flush(times = 24): Promise<void> {
  for (let i = 0; i < times; i++) await Promise.resolve();
  flushSync();
}

function control(key: string): HTMLInputElement | HTMLTextAreaElement {
  return target.querySelector(`[data-testid="note-field-${key}"]`) as HTMLInputElement | HTMLTextAreaElement;
}

function button(key: string, action: string): HTMLButtonElement {
  return target.querySelector(`[data-testid="note-field-${key}-format-${action}"]`) as HTMLButtonElement;
}

function press(el: HTMLElement, init: KeyboardEventInit): KeyboardEvent {
  const event = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init });
  el.dispatchEvent(event);
  return event;
}

async function clickAction(key: string, action: string): Promise<boolean> {
  const el = button(key, action);
  const down = new MouseEvent('mousedown', { bubbles: true, cancelable: true });
  el.dispatchEvent(down);
  el.click();
  await flush();
  return down.defaultPrevented;
}

afterEach(() => {
  if (instance) unmount(instance);
  instance = null;
  document.body.innerHTML = '';
});

describe('Markdown formatting toolbar', () => {
  it('wraps the selection and keeps focus in the field', async () => {
    setLocale('en');
    mountKind('basic', { front: 'Hello world' });
    const area = control('front') as HTMLTextAreaElement;
    area.focus();
    area.setSelectionRange(0, 5);

    // The button cancels mousedown so the textarea never loses focus or its selection before the click.
    expect(await clickAction('front', 'bold')).toBe(true);
    expect(fields.front).toBe('**Hello** world');
    expect(area.value).toBe('**Hello** world');
    expect([area.selectionStart, area.selectionEnd]).toEqual([2, 7]);
    expect(document.activeElement).toBe(area);
  });

  it('removes the pair on a second press and keeps the inner text selected', async () => {
    setLocale('en');
    mountKind('basic', { front: 'Hello world' });
    const area = control('front') as HTMLTextAreaElement;
    area.focus();
    area.setSelectionRange(0, 5);

    await clickAction('front', 'bold');
    await clickAction('front', 'bold');

    expect(fields.front).toBe('Hello world');
    expect(area.value).toBe('Hello world');
    expect([area.selectionStart, area.selectionEnd]).toEqual([0, 5]);
  });

  it('inserts the pair at an empty selection and puts the caret between the markers', async () => {
    setLocale('en');
    mountKind('basic', { front: '' });
    const area = control('front') as HTMLTextAreaElement;
    area.focus();
    area.setSelectionRange(0, 0);

    await clickAction('front', 'italic');

    expect(fields.front).toBe('**');
    expect(area.value).toBe('**');
    expect([area.selectionStart, area.selectionEnd]).toEqual([1, 1]);
  });

  it('wraps and unwraps strikethrough from its own button', async () => {
    setLocale('en');
    mountKind('basic', { front: 'gone' });
    const area = control('front') as HTMLTextAreaElement;
    area.focus();
    area.setSelectionRange(0, 4);

    await clickAction('front', 'strike');
    expect(fields.front).toBe('~~gone~~');
    expect([area.selectionStart, area.selectionEnd]).toEqual([2, 6]);

    await clickAction('front', 'strike');
    expect(fields.front).toBe('gone');
    expect([area.selectionStart, area.selectionEnd]).toEqual([0, 4]);
  });

  it('formats only single-string controls, so lines has no toolbar', async () => {
    setLocale('en');
    mountKind('list');
    expect(target.querySelector('[data-testid="note-field-prompt-format-toolbar"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="note-field-items-format-toolbar"]')).toBeNull();
    expect(target.querySelector('[data-testid="note-field-ordered-format-toolbar"]')).toBeNull();
  });

  it('formats a text input (source_url) as well as a textarea', async () => {
    setLocale('en');
    mountKind('basic', { source_url: 'example.com' });
    const input = control('source_url') as HTMLInputElement;
    input.focus();
    input.setSelectionRange(0, 11);

    await clickAction('source_url', 'bold');

    expect(fields.source_url).toBe('**example.com**');
    expect([input.selectionStart, input.selectionEnd]).toEqual([2, 13]);
  });

  it('reports the new caret through oncaret', async () => {
    setLocale('en');
    const oncaret = vi.fn();
    mountKind('basic', { front: 'Hi' }, { oncaret });
    const area = control('front') as HTMLTextAreaElement;
    area.focus();
    area.setSelectionRange(0, 2);

    await clickAction('front', 'bold');

    expect(oncaret).toHaveBeenCalledWith('front', 2, 4);
  });

  it('keeps the image and shortcode actions on the same field', async () => {
    setLocale('en');
    const oninsertimage = vi.fn();
    const oninsertshortcode = vi.fn();
    mountKind('basic', {}, { oninsertimage, oninsertshortcode });
    expect(target.querySelector('[data-testid="note-field-front-toolbar"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="note-field-front-format-toolbar"]')).toBeTruthy();

    (target.querySelector('[data-testid="note-field-front-insert-image"]') as HTMLButtonElement).click();
    (target.querySelector('[data-testid="note-field-front-insert-shortcode"]') as HTMLButtonElement).click();

    expect(oninsertimage).toHaveBeenCalledWith('front', expect.any(HTMLButtonElement));
    expect(oninsertshortcode).toHaveBeenCalledWith('front', expect.any(HTMLButtonElement));
  });
});

describe('Markdown formatting shortcuts', () => {
  it('wraps with Ctrl+B and prevents the browser default', async () => {
    setLocale('en');
    mountKind('basic', { front: 'Hello' });
    const area = control('front') as HTMLTextAreaElement;
    area.focus();
    area.setSelectionRange(0, 5);

    const event = press(area, { key: 'b', ctrlKey: true });
    await flush();

    expect(event.defaultPrevented).toBe(true);
    expect(fields.front).toBe('**Hello**');
    expect([area.selectionStart, area.selectionEnd]).toEqual([2, 7]);
    expect(document.activeElement).toBe(area);
  });

  it('wraps with Cmd+Shift+X for strikethrough and prevents the browser default', async () => {
    setLocale('en');
    mountKind('basic', { front: 'gone' });
    const area = control('front') as HTMLTextAreaElement;
    area.focus();
    area.setSelectionRange(0, 4);

    const event = press(area, { key: 'X', metaKey: true, shiftKey: true });
    await flush();

    expect(event.defaultPrevented).toBe(true);
    expect(fields.front).toBe('~~gone~~');
  });

  it('leaves a plain letter and an unowned combination to the browser', async () => {
    setLocale('en');
    mountKind('basic', { front: 'Hello' });
    const area = control('front') as HTMLTextAreaElement;
    area.focus();
    area.setSelectionRange(0, 5);

    expect(press(area, { key: 'b' }).defaultPrevented).toBe(false);
    expect(press(area, { key: 'b', ctrlKey: true, shiftKey: true }).defaultPrevented).toBe(false);
    expect(press(area, { key: 'u', ctrlKey: true }).defaultPrevented).toBe(false);
    await flush();

    expect(fields.front).toBe('Hello');
    expect(area.value).toBe('Hello');
  });

  it('does not format a lines field, whose offsets do not map onto the stored array', async () => {
    setLocale('en');
    mountKind('list', { items: ['one', 'two'] });
    const area = control('items') as HTMLTextAreaElement;
    area.focus();
    area.setSelectionRange(0, 3);

    const event = press(area, { key: 'b', ctrlKey: true });
    await flush();

    expect(event.defaultPrevented).toBe(false);
    expect(fields.items).toEqual(['one', 'two']);
    expect(area.value).toBe('one\ntwo');
  });
});
