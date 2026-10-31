import { describe, it, expect, afterEach, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { mount, unmount, flushSync } from 'svelte';
import ImageOcclusionEditor from '../lib/components/ImageOcclusionEditor.svelte';
import {
  Ellipse,
  Polygon,
  Rectangle,
  Text,
  extractShapesFromRenderedClozes,
  type Shape,
} from '../cardframe/image-occlusion';
import {
  documentMode,
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

interface GoldenCard {
  ordinal: number;
  question_html: string;
  answer_html: string;
}

interface GoldenNote {
  guid: string;
  fields: Record<string, string>;
  cards: GoldenCard[];
}

interface Golden {
  notes: GoldenNote[];
}

function occlusionField(note: GoldenNote): string {
  const field = Object.values(note.fields).find((value) => value.includes('image-occlusion:'));
  if (field === undefined) throw new Error(`note ${note.guid} has no image-occlusion field`);
  return field;
}

/**
 * The image-occlusion fixture from the Anki oracle: one note per shape kind, a group of two
 * shapes under one cloze number, and a note whose shapes carry `oi=1`.
 *
 * `oi=1` is Anki's inactive-mask flag, so that note is "hide all, guess one": the question
 * shows the inactive masks and hides only the shape under test. A note without the flag is
 * "hide one, guess one", where every other shape stays visible. The fixture was produced
 * through Anki's own image-occlusion API, so the fields and the rendered faces are Anki's.
 */
const FIXTURE = JSON.parse(
  readFileSync(join(GOLDEN_DIR, 'image-occlusion.golden.json'), 'utf8'),
) as Golden;

const FIXTURE_NOTES = FIXTURE.notes.map((note) => ({ guid: note.guid, field: occlusionField(note), note }));

/** The shape class Anki's runtime builds for each stored kind. */
const SHAPE_CLASSES = { rect: Rectangle, ellipse: Ellipse, polygon: Polygon, text: Text } as const;

function shapeSnapshot(shape: Shape): Record<string, unknown> {
  const snapshot: Record<string, unknown> = {
    left: shape.left,
    top: shape.top,
    ordinal: shape.ordinal,
    occludeInactive: shape.occludeInactive,
  };
  if (shape instanceof Rectangle) {
    snapshot.width = shape.width;
    snapshot.height = shape.height;
  } else if (shape instanceof Ellipse) {
    snapshot.rx = shape.rx;
    snapshot.ry = shape.ry;
  } else if (shape instanceof Polygon) {
    snapshot.points = shape.points.map((point) => ({ x: point.x, y: point.y }));
  } else if (shape instanceof Text) {
    snapshot.text = shape.text;
    snapshot.scaleX = shape.scaleX;
    snapshot.scaleY = shape.scaleY;
    snapshot.fontSize = shape.fontSize;
  }
  return snapshot;
}

/** The same snapshot for a shape the editor's field model read. */
function editorSnapshot(shape: ReturnType<typeof documentShapes>[number]): Record<string, unknown> {
  const geometry = shapeGeometry(shape);
  const snapshot: Record<string, unknown> = {
    left: geometry.left,
    top: geometry.top,
    ordinal: shape.ordinal,
    occludeInactive: geometry.occludeInactive ?? false,
  };
  if (shape.kind === 'rect') {
    snapshot.width = geometry.width;
    snapshot.height = geometry.height;
  } else if (shape.kind === 'ellipse') {
    snapshot.rx = geometry.rx;
    snapshot.ry = geometry.ry;
  } else if (shape.kind === 'polygon') {
    snapshot.points = (geometry.points ?? []).map((point) => ({ x: point.x, y: point.y }));
  } else {
    snapshot.text = geometry.text;
    snapshot.scaleX = geometry.scale;
    snapshot.scaleY = geometry.scale;
    snapshot.fontSize = geometry.fontSize;
  }
  return snapshot;
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

describe('image occlusion Anki fixture', () => {
  it.each(FIXTURE_NOTES)('$guid: the editor model matches every face Anki rendered', ({ field, note }) => {
    const editorShapes = documentShapes(parseOcclusionField(field));
    const expected = editorShapes.map(editorSnapshot);

    for (const card of note.cards) {
      const cardCloze = card.ordinal + 1;
      for (const [side, html, activeSelector] of [
        ['question', card.question_html, '.cloze'],
        ['answer', card.answer_html, '.cloze-highlight'],
      ] as const) {
        const rendered = document.createElement('div');
        rendered.innerHTML = html;

        // The face's classes divide the shapes into the deletion under test and the
        // inactive shapes. The editor's field model must produce the same division.
        expect(
          extractShapesFromRenderedClozes(rendered, activeSelector).map((shape) => shape.ordinal),
          `${side} active ordinals`,
        ).toEqual(editorShapes.filter((shape) => shape.ordinal === cardCloze).map((shape) => shape.ordinal));
        expect(
          extractShapesFromRenderedClozes(rendered, '.cloze-inactive').map((shape) => shape.ordinal),
          `${side} inactive ordinals`,
        ).toEqual(editorShapes.filter((shape) => shape.ordinal !== cardCloze).map((shape) => shape.ordinal));

        // Document order is field order, so the geometry can be compared shape by shape.
        const renderedShapes = extractShapesFromRenderedClozes(
          rendered,
          '.cloze, .cloze-inactive, .cloze-highlight',
        );
        expect(renderedShapes, `${side} shape count`).toHaveLength(editorShapes.length);
        renderedShapes.forEach((shape, index) => {
          const editorShape = editorShapes[index]!;
          expect(shape, `${side} shape ${index} class`).toBeInstanceOf(SHAPE_CLASSES[editorShape.kind]);
          expect(shapeSnapshot(shape), `${side} shape ${index} geometry`).toEqual(expected[index]);
        });
      }
    }
  });

  it.each(FIXTURE_NOTES)('$guid: applying the existing number saves the field byte for byte', async ({ field }) => {
    const { onchange } = mountEditor(field);
    await flush();
    expect(onchange).not.toHaveBeenCalled();

    const lastOrdinal = documentShapes(parseOcclusionField(field)).at(-1)!.ordinal;
    setNumber('occlusion-cloze', lastOrdinal);
    click('occlusion-apply-cloze');
    await flush();
    expect(lastValue(onchange)).toBe(field);
  });

  it.each(FIXTURE_NOTES)('$guid: re-applying the recorded mode saves the field byte for byte', async ({ field }) => {
    const { onchange } = mountEditor(field);
    await flush();

    const mode = documentMode(parseOcclusionField(field));
    setSelect('occlusion-mode', mode);
    await flush();
    expect(lastValue(onchange)).toBe(field);
  });
});
