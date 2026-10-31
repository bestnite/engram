import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  assignOrdinal,
  createShape,
  documentMode,
  documentShapes,
  firstImageSource,
  formatCoordinate,
  parseOcclusionField,
  serializeOcclusionField,
  setDocumentMode,
  setShapeGeometry,
  shapeGeometry,
  type OcclusionDocument,
  type OcclusionMode,
  type OcclusionPart,
  type OcclusionShapeKind,
} from '../lib/image-occlusion/markup';
import { geometryFromObject } from '../lib/image-occlusion/geometry';

/**
 * The stored form of an image-occlusion note.
 *
 * The round-trip cases are the acceptance rule "reopening and saving without a change is byte-identical":
 * the parser keeps the original text of every deletion it did not touch, so Anki's own spellings survive
 * even when this module would format the same values differently.
 */

/** One shape exactly as the Anki fixture in internal/notetype/testdata/anki/stock.golden.json stores it. */
const ANKI_FIELD = '{{c1::image-occlusion:rect:left=0.1:top=0.2:width=0.3:height=0.4:oi=1}}';

describe('image-occlusion markup', () => {
  it('round-trips the Anki fixture byte for byte', () => {
    const document = parseOcclusionField(ANKI_FIELD);
    expect(serializeOcclusionField(document)).toBe(ANKI_FIELD);
  });

  it('reads the fixture as one rect assigned to cloze 1 in hide-all mode', () => {
    const document = parseOcclusionField(ANKI_FIELD);
    const shapes = documentShapes(document);
    const shape = shapes[0]!;
    expect(shapes).toHaveLength(1);
    expect(shape.kind).toBe('rect');
    expect(shape.ordinal).toBe(1);
    expect(documentMode(document)).toBe('hideAll');
    expect(shapeGeometry(shape)).toMatchObject({
      left: 0.1,
      top: 0.2,
      width: 0.3,
      height: 0.4,
      occludeInactive: true,
    });
  });

  it('round-trips every shape kind, including escaped text, without rewriting it', () => {
    const field = [
      '{{c1::image-occlusion:rect:left=0.1:top=0.2:width=0.3:height=0.4:oi=1}}',
      '{{c2::image-occlusion:ellipse:left=0.5:top=0.1:rx=0.1:ry=0.08}}',
      '{{c3::image-occlusion:polygon:left=0.1:top=0.5:points=0.1,0.5 0.28,0.5 0.19,0.64}}',
      '{{c4::image-occlusion:text:left=10:top=20:text=\\:l\\\\ol\\::width=30:height=40}}',
    ].join('');
    const document = parseOcclusionField(field);
    expect(documentShapes(document).map((shape) => shape.kind)).toEqual(['rect', 'ellipse', 'polygon', 'text']);
    expect(documentShapes(document)[3]!.properties.find((property) => property.name === 'text')?.value).toBe(':l\\ol:');
    expect(serializeOcclusionField(document)).toBe(field);
  });

  it('round-trips a deletion that addresses two cloze numbers', () => {
    const field = '{{c1,2::image-occlusion:rect:left=10.0:top=20:width=30:height=10}}';
    const document = parseOcclusionField(field);
    expect(documentShapes(document)[0]!.ordinal).toBe(1);
    expect(serializeOcclusionField(document)).toBe(field);
  });

  it('keeps surrounding text and ordinary clozes literal', () => {
    const field = 'before {{c1::plain answer}} {{c0::image-occlusion:rect:left=0.1}} after';
    const document = parseOcclusionField(field);
    expect(serializeOcclusionField(document)).toBe(field);
    expect(documentShapes(document)).toHaveLength(1);
  });

  it('creates two groups of shapes in Anki form', () => {
    const rect = createShape('rect', 1, { left: 0.1, top: 0.2, width: 0.3, height: 0.4 });
    const ellipseA = createShape('ellipse', 2, { left: 0.5, top: 0.1, rx: 0.1, ry: 0.08 });
    const ellipseB = createShape('ellipse', 2, { left: 0.6, top: 0.3, rx: 0.05, ry: 0.04 });
    const field = serializeOcclusionField({
      parts: [
        { type: 'shape', shape: rect },
        { type: 'shape', shape: ellipseA },
        { type: 'shape', shape: ellipseB },
      ],
    });
    expect(field).toBe(
      '{{c1::image-occlusion:rect:left=0.1:top=0.2:width=0.3:height=0.4}}' +
        '{{c2::image-occlusion:ellipse:left=0.5:top=0.1:rx=0.1:ry=0.08}}' +
        '{{c2::image-occlusion:ellipse:left=0.6:top=0.3:rx=0.05:ry=0.04}}',
    );
  });

  it('groups existing shapes under one number without touching the others', () => {
    const field =
      '{{c1::image-occlusion:rect:left=0.1:top=0.1:width=0.1:height=0.1}}' +
      '{{c2::image-occlusion:ellipse:left=0.5:top=0.5:rx=0.1:ry=0.1}}';
    const document = parseOcclusionField(field);
    const [first, second] = documentShapes(document);
    assignOrdinal(first!, 3);
    assignOrdinal(second!, 3);
    expect(serializeOcclusionField(document)).toBe(
      '{{c3::image-occlusion:rect:left=0.1:top=0.1:width=0.1:height=0.1}}' +
        '{{c3::image-occlusion:ellipse:left=0.5:top=0.5:rx=0.1:ry=0.1}}',
    );
  });

  it('writes a moved shape back in place and preserves unknown properties', () => {
    const document = parseOcclusionField('{{c1::image-occlusion:rect:left=0.1:top=0.2:width=0.3:height=0.4:custom=x}}');
    const [shape] = documentShapes(document);
    setShapeGeometry(shape!, { left: 0.5, top: 0.25, width: 0.3, height: 0.4 });
    expect(serializeOcclusionField(document)).toBe(
      '{{c1::image-occlusion:rect:left=0.5:top=0.25:width=0.3:height=0.4:custom=x}}',
    );
  });

  it('switches the mode by adding or removing oi', () => {
    const document = parseOcclusionField(ANKI_FIELD);
    setDocumentMode(document, 'hideOne');
    expect(serializeOcclusionField(document)).toBe(
      '{{c1::image-occlusion:rect:left=0.1:top=0.2:width=0.3:height=0.4}}',
    );
    setDocumentMode(document, 'hideAll');
    expect(serializeOcclusionField(document)).toBe(ANKI_FIELD);
  });

  it('formats coordinates without a float tail', () => {
    expect(formatCoordinate(0.1 + 0.2)).toBe('0.3');
    expect(formatCoordinate(-0)).toBe('0');
  });

  it('finds the first image source and nothing for a field without one', () => {
    expect(firstImageSource('<div><img class="x" src="/fm/token/a.png" /></div>')).toBe('/fm/token/a.png');
    expect(firstImageSource("<img src='b.png'>")).toBe('b.png');
    expect(firstImageSource('<p>no image</p>')).toBe('');
  });
});

/**
 * The editor has to reproduce every shape Anki writes, not only the rectangle in the stock
 * fixture. The golden under test records one note per shape kind, a group of two shapes
 * under one cloze number, and a note whose shapes carry `oi=1`.
 *
 * `oi=1` is Anki's inactive-mask flag: a note that carries it on its shapes is in the
 * "hide all, guess one" mode, because every other shape stays visible as a mask. A note
 * without it is in "hide one, guess one" mode. The golden's rendered faces show the same
 * division: the face under test is the active deletion and the shapes marked `oi=1` are
 * rendered as inactive masks.
 */
interface GoldenNote {
  guid: string;
  fields: Record<string, string>;
}

interface Golden {
  notes: GoldenNote[];
}

const GOLDEN_DIR = join(import.meta.dirname, '../../../internal/notetype/testdata/anki');

function occlusionField(note: GoldenNote): string {
  const field = Object.values(note.fields).find((value) => value.includes('image-occlusion:'));
  if (field === undefined) throw new Error(`note ${note.guid} has no image-occlusion field`);
  return field;
}

const FIXTURE_KINDS: Record<string, OcclusionShapeKind[]> = {
  'zz-occlusion-rect': ['rect'],
  'zz-occlusion-ellipse': ['ellipse'],
  'zz-occlusion-polygon': ['polygon'],
  'zz-occlusion-text': ['text'],
  'zz-occlusion-group': ['rect', 'ellipse'],
  'zz-occlusion-hide-all': ['rect', 'ellipse'],
};

const FIXTURE_NOTES = (
  JSON.parse(readFileSync(join(GOLDEN_DIR, 'image-occlusion.golden.json'), 'utf8')) as Golden
).notes.map((note) => ({
  guid: note.guid,
  field: occlusionField(note),
  kinds: FIXTURE_KINDS[note.guid] ?? [],
  mode: (note.guid === 'zz-occlusion-hide-all' ? 'hideAll' : 'hideOne') as OcclusionMode,
}));

/** Rebuilds a field from the shapes the parser read, using the editor's public constructors. */
function rebuildField(field: string): string {
  const parsed = parseOcclusionField(field);
  const parts: OcclusionPart[] = parsed.parts.map((part) => {
    if (part.type === 'text') return part;
    const shape = part.shape;
    return { type: 'shape', shape: createShape(shape.kind, shape.ordinal, shapeGeometry(shape)) };
  });
  const rebuilt: OcclusionDocument = { parts };
  return serializeOcclusionField(rebuilt);
}

describe('image-occlusion Anki fixture', () => {
  it.each(FIXTURE_NOTES)('$guid: opening and saving without an edit is byte-identical', ({ field }) => {
    expect(serializeOcclusionField(parseOcclusionField(field))).toBe(field);
  });

  it.each(FIXTURE_NOTES)('$guid: the shape model writes the same field Anki stored', ({ field }) => {
    expect(rebuildField(field)).toBe(field);
  });

  it.each(FIXTURE_NOTES)('$guid: reads the recorded shape kinds and mode', ({ field, kinds, mode }) => {
    const document = parseOcclusionField(field);
    expect(documentShapes(document).map((shape) => shape.kind)).toEqual(kinds);
    expect(documentMode(document)).toBe(mode);
  });
});

describe('image-occlusion canvas conversion', () => {
  const size = { width: 400, height: 400 };

  it('reads a moved and resized rectangle from its centre and scale', () => {
    expect(
      geometryFromObject(
        'rect',
        { center: { x: 200, y: 100 }, width: 100, height: 100, scaleX: 2, scaleY: 1, angle: 0 },
        size,
      ),
    ).toMatchObject({ left: 0.25, top: 0.125, width: 0.5, height: 0.25 });
  });

  it('reads an ellipse from its scaled radii', () => {
    expect(
      geometryFromObject(
        'ellipse',
        { center: { x: 200, y: 200 }, width: 100, height: 50, scaleX: 1, scaleY: 1, angle: 0, rx: 50, ry: 25 },
        size,
      ),
    ).toMatchObject({ left: 0.375, top: 0.4375, rx: 0.125, ry: 0.0625 });
  });

  it('reads a polygon as absolute vertices and reports their top-left corner', () => {
    const geometry = geometryFromObject(
      'polygon',
      {
        center: { x: 150, y: 100 },
        width: 100,
        height: 50,
        scaleX: 1,
        scaleY: 1,
        angle: 0,
        points: [
          { x: 0, y: 0 },
          { x: 100, y: 0 },
          { x: 50, y: 50 },
        ],
        pathOffset: { x: 50, y: 25 },
      },
      size,
    );
    expect(geometry.left).toBe(0.25);
    expect(geometry.top).toBe(0.1875);
    expect(geometry.points).toEqual([
      { x: 0.25, y: 0.1875 },
      { x: 0.5, y: 0.1875 },
      { x: 0.375, y: 0.3125 },
    ]);
  });

  it('reads a text label with its scaled font size', () => {
    expect(
      geometryFromObject(
        'text',
        { center: { x: 200, y: 100 }, width: 100, height: 50, scaleX: 2, scaleY: 1, angle: 15, text: 'hi', fontSize: 20 },
        size,
      ),
    ).toMatchObject({ left: 0.25, top: 0.1875, text: 'hi', scale: 2, fontSize: 0.05, angle: 15 });
  });
});
