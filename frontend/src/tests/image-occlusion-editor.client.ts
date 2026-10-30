import { describe, it, expect, afterEach, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { mount, unmount, flushSync } from 'svelte';
import ImageOcclusionEditor from '../lib/components/ImageOcclusionEditor.svelte';
import { extractShapesFromRenderedClozes, Rectangle } from '../cardframe/image-occlusion';
import {
  documentShapes,
  parseOcclusionField,
  serializeOcclusionField,
  shapeGeometry,
} from '../lib/image-occlusion/markup';

/**
 * The mask editor as the note editor uses it.
 *
 * happy-dom has no canvas paint API, so the drawing library cannot create its surface and the component
 * falls back to the model controls. The cases below drive those controls, which is the same path a user
 * without a working canvas takes; the pointer path (move and resize) is covered by the plain-object
 * conversion cases in image-occlusion-markup.test.ts.
 */

const GOLDEN_DIR = join(import.meta.dirname, '../../../internal/notetype/testdata/anki');
const ANKI_FIELD = '{{c1::image-occlusion:rect:left=0.1:top=0.2:width=0.3:height=0.4:oi=1}}';

interface GoldenNote {
  fields: Record<string, string>;
  cards: Array<{ question_html: string }>;
}

let target: HTMLElement | null = null;
let instance: ReturnType<typeof mount> | null = null;

async function flush(times = 24): Promise<void> {
  for (let i = 0; i < times; i += 1) await Promise.resolve();
  flushSync();
}

function mountEditor(value: string, onchange = vi.fn()): { onchange: ReturnType<typeof vi.fn>; element: HTMLElement } {
  target = document.createElement('div');
  document.body.appendChild(target);
  instance = mount(ImageOcclusionEditor, { target, props: { value, imageSrc: '', onchange } });
  return { onchange, element: target };
}

function click(testid: string): void {
  const element = target?.querySelector(`[data-testid="${testid}"]`) as HTMLElement | null;
  if (!element) throw new Error(`missing ${testid}`);
  element.click();
}

function setNumber(testid: string, value: number): void {
  const input = target?.querySelector(`[data-testid="${testid}"]`) as HTMLInputElement | null;
  if (!input) throw new Error(`missing ${testid}`);
  input.value = String(value);
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

function setSelect(testid: string, value: string): void {
  const select = target?.querySelector(`[data-testid="${testid}"]`) as HTMLSelectElement | null;
  if (!select) throw new Error(`missing ${testid}`);
  select.value = value;
  select.dispatchEvent(new Event('change', { bubbles: true }));
}

function lastValue(onchange: ReturnType<typeof vi.fn>): string {
  return String(onchange.mock.calls[onchange.mock.calls.length - 1]?.[0] ?? '');
}

afterEach(() => {
  if (instance) unmount(instance);
  instance = null;
  target = null;
  document.body.innerHTML = '';
});

describe('image occlusion editor', () => {
  it('renders the Anki fixture the way the review runtime reads it', () => {
    const golden = JSON.parse(readFileSync(join(GOLDEN_DIR, 'stock.golden.json'), 'utf8')) as { notes: GoldenNote[] };
    const note = golden.notes.find((entry) => Object.values(entry.fields).some((field) => field.includes('image-occlusion:')));
    expect(note).toBeTruthy();

    const field = Object.values(note!.fields).find((value) => value.includes('image-occlusion:'))!;
    const stored = documentShapes(parseOcclusionField(field))[0]!;
    const geometry = shapeGeometry(stored);
    const fromEditor = new Rectangle({
      left: geometry.left,
      top: geometry.top,
      width: geometry.width,
      height: geometry.height,
      occludeInactive: geometry.occludeInactive,
      ordinal: stored.ordinal,
    });

    const rendered = document.createElement('div');
    rendered.innerHTML = note!.cards[0]!.question_html;
    const fromServer = extractShapesFromRenderedClozes(rendered, '.cloze');
    expect(fromServer).toHaveLength(1);
    expect(fromServer[0]).toBeInstanceOf(Rectangle);
    expect(fromEditor).toMatchObject({
      left: (fromServer[0] as Rectangle).left,
      top: (fromServer[0] as Rectangle).top,
      width: (fromServer[0] as Rectangle).width,
      height: (fromServer[0] as Rectangle).height,
      occludeInactive: (fromServer[0] as Rectangle).occludeInactive,
      ordinal: (fromServer[0] as Rectangle).ordinal,
    });
    expect(serializeOcclusionField(parseOcclusionField(field))).toBe(field);
  });

  it('leaves an untouched field alone: mounting and applying the same number emits the same bytes', async () => {
    const { onchange } = mountEditor(ANKI_FIELD);
    await flush();
    expect(onchange).not.toHaveBeenCalled();

    // Assigning the number the shape already carries is a no-op on the stored form.
    click('occlusion-apply-cloze');
    await flush();
    expect(onchange).toHaveBeenCalled();
    expect(lastValue(onchange)).toBe(ANKI_FIELD);
  });

  it('draws shapes and groups two of them under one cloze number', async () => {
    const { onchange } = mountEditor('');
    await flush();

    click('occlusion-add-rect');
    click('occlusion-add-ellipse');
    await flush();

    let field = lastValue(onchange);
    expect(field).toBe(
      '{{c1::image-occlusion:rect:left=0.1:top=0.1:width=0.2:height=0.12}}' +
        '{{c1::image-occlusion:ellipse:left=0.58:top=0.13:rx=0.1:ry=0.08}}',
    );

    // Adding leaves the new shape selected. Deselect the ellipse, select the rectangle, and put it
    // under number 2; the ellipse keeps number 1.
    click('occlusion-shape-1');
    click('occlusion-shape-0');
    setNumber('occlusion-cloze', 2);
    click('occlusion-apply-cloze');
    await flush();
    field = lastValue(onchange);
    expect(field).toContain('{{c2::image-occlusion:rect:');
    expect(field).toContain('{{c1::image-occlusion:ellipse:');

    // Select both and group them under number 2.
    click('occlusion-shape-1');
    click('occlusion-apply-cloze');
    await flush();
    field = lastValue(onchange);
    expect(field).not.toContain('{{c1::');
    expect(field.match(/\{\{c2::image-occlusion:/g)).toHaveLength(2);

    // The stored field round-trips through the parser without losing the group.
    const parsed = documentShapes(parseOcclusionField(field));
    expect(parsed.map((shape) => shape.ordinal)).toEqual([2, 2]);
  });

  it('writes the chosen occlusion mode into every shape', async () => {
    const { onchange } = mountEditor(ANKI_FIELD);
    await flush();

    setSelect('occlusion-mode', 'hideOne');
    await flush();
    expect(lastValue(onchange)).toBe('{{c1::image-occlusion:rect:left=0.1:top=0.2:width=0.3:height=0.4}}');

    setSelect('occlusion-mode', 'hideAll');
    await flush();
    expect(lastValue(onchange)).toBe(ANKI_FIELD);
  });
});
