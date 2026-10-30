/**
 * The stored form of an image-occlusion field.
 *
 * The server (`internal/notetype/cloze.go`) parses a cloze deletion whose text starts with
 * `image-occlusion:` as one shape: `{{c1::image-occlusion:rect:left=0.1:top=0.2:width=0.3:height=0.4:oi=1}}`.
 * Coordinates are normalized against the image, so the same field renders on any display size. A group is
 * several deletions that carry the same number, and `oi=1` marks a shape as an inactive mask, which is the
 * note's "hide all, guess one" mode.
 *
 * The editor keeps the original text for every deletion it did not touch. Reopening a note and saving it
 * unchanged therefore writes exactly the bytes that were read, not a normalized rewrite: Anki writes some
 * property spellings and orders that this module does not reproduce, and rewriting them would turn a no-op
 * edit into a diff.
 */

export type OcclusionShapeKind = 'rect' | 'ellipse' | 'polygon' | 'text';

/** The two modes an Anki image-occlusion note stores. */
export type OcclusionMode = 'hideOne' | 'hideAll';

const IMAGE_PREFIX = 'image-occlusion:';
const OCClUDE_INACTIVE = 'oi';
const DEFAULT_FILL = '#ffeba2';
const ANGLE_STEPS = 10000;

export interface OcclusionPoint {
  x: number;
  y: number;
}

/** The geometry of one shape, normalized against the image unless stated otherwise. */
export interface OcclusionGeometry {
  left: number;
  top: number;
  width?: number;
  height?: number;
  rx?: number;
  ry?: number;
  points?: OcclusionPoint[];
  text?: string;
  /** Text shapes store one uniform scale factor. */
  scale?: number;
  /** Text font size normalized against the image height. */
  fontSize?: number;
  /** Rotation in degrees. The stored form is a fraction of a full turn. */
  angle?: number;
  fill?: string;
  occludeInactive?: boolean;
}

export interface OcclusionProperty {
  name: string;
  value: string;
}

export interface OcclusionShape {
  /** Editor-only identity; never written to the field. */
  id: string;
  kind: OcclusionShapeKind;
  ordinal: number;
  properties: OcclusionProperty[];
  /**
   * The deletion body as read (`rect:left=0.1:...`). It is written back verbatim while the shape is clean,
   * which is what keeps an untouched field byte-identical.
   */
  rawBody?: string;
  /** The ordinal text as read (`1` or `1,2`). Written back verbatim while the shape is clean. */
  rawOrdinals?: string;
  /** Whether this shape was changed since it was read. */
  dirty: boolean;
}

export type OcclusionPart =
  | { type: 'text'; text: string }
  | { type: 'shape'; shape: OcclusionShape };

export interface OcclusionDocument {
  parts: OcclusionPart[];
}

let nextShapeId = 1;

function newShapeId(): string {
  return `occlusion-shape-${nextShapeId++}`;
}

/** Parses an ordinal list such as `1,2`; an empty or non-numeric list yields no ordinals. */
export function parseOrdinals(text: string): number[] {
  const ordinals: number[] = [];
  for (const part of text.split(',')) {
    const value = Number.parseInt(part, 10);
    if (Number.isFinite(value) && value >= 0 && !ordinals.includes(value)) ordinals.push(value);
  }
  return ordinals.sort((a, b) => a - b);
}

/** Renders an ordinal list the way Anki writes it. */
export function ordinalsText(ordinals: readonly number[]): string {
  return [...new Set(ordinals)].sort((a, b) => a - b).join(',');
}

/**
 * Decodes a property value. Anki escapes `:` and `\` with a backslash, because both are structural
 * characters of the shape grammar.
 */
export function decodePropertyValue(text: string): string {
  let out = '';
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (char === '\\' && index + 1 < text.length && (text[index + 1] === '\\' || text[index + 1] === ':')) {
      out += text[index + 1];
      index += 1;
      continue;
    }
    out += char;
  }
  return out;
}

/** Encodes a property value for the field. */
export function encodePropertyValue(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/:/g, '\\:');
}

/**
 * Splits `rect:left=0.1:top=0.2` into the shape kind and its properties. A body without a `:` is not a
 * shape, and the scanner leaves such a deletion alone as plain text.
 */
export function parseShapeBody(body: string): { kind: OcclusionShapeKind; properties: OcclusionProperty[] } | null {
  const separator = body.indexOf(':');
  if (separator < 0) return null;
  const kind = body.slice(0, separator);
  if (kind !== 'rect' && kind !== 'ellipse' && kind !== 'polygon' && kind !== 'text') return null;

  const properties: OcclusionProperty[] = [];
  let index = separator;
  while (index < body.length && body[index] === ':') {
    const nameStart = index + 1;
    let nameEnd = nameStart;
    while (nameEnd < body.length && body[nameEnd] !== '=') nameEnd += 1;
    if (nameEnd === nameStart || nameEnd === body.length) break;
    const name = body.slice(nameStart, nameEnd);

    let value = '';
    index = nameEnd + 1;
    while (index < body.length && body[index] !== ':') {
      const char = body[index];
      if (char === '\\' && index + 1 < body.length && (body[index + 1] === '\\' || body[index + 1] === ':')) {
        value += body[index + 1];
        index += 2;
        continue;
      }
      value += char;
      index += 1;
    }
    properties.push({ name, value });
  }
  return { kind, properties };
}

function makeShape(
  kind: OcclusionShapeKind,
  ordinal: number,
  properties: OcclusionProperty[],
  rawBody: string,
  rawOrdinals: string,
): OcclusionShape {
  return {
    id: newShapeId(),
    kind,
    ordinal,
    properties: properties.map((property) => ({ ...property })),
    rawBody,
    rawOrdinals,
    dirty: false,
  };
}

/**
 * Reads a field value into text and shape parts. Anything that is not an image-occlusion deletion stays
 * literal text, including ordinary cloze deletions, so a field that also holds prose survives the round trip.
 */
export function parseOcclusionField(value: string): OcclusionDocument {
  const parts: OcclusionPart[] = [];
  let cursor = 0;
  while (cursor < value.length) {
    const open = value.indexOf('{{c', cursor);
    if (open < 0) {
      parts.push({ type: 'text', text: value.slice(cursor) });
      cursor = value.length;
      break;
    }
    const marker = /^\{\{c([0-9]+(?:,[0-9]+)*)::/.exec(value.slice(open));
    if (!marker) {
      // Not a cloze marker: keep the "{{c" and keep scanning after it.
      parts.push({ type: 'text', text: value.slice(cursor, open + 3) });
      cursor = open + 3;
      continue;
    }
    const bodyStart = open + marker[0].length;
    const close = value.indexOf('}}', bodyStart);
    if (close < 0) {
      parts.push({ type: 'text', text: value.slice(cursor) });
      cursor = value.length;
      break;
    }
    const body = value.slice(bodyStart, close);
    if (!body.startsWith(IMAGE_PREFIX)) {
      // An ordinary cloze deletion is literal text for this editor.
      parts.push({ type: 'text', text: value.slice(cursor, close + 2) });
      cursor = close + 2;
      continue;
    }
    const parsed = parseShapeBody(body.slice(IMAGE_PREFIX.length));
    if (!parsed) {
      parts.push({ type: 'text', text: value.slice(cursor, close + 2) });
      cursor = close + 2;
      continue;
    }
    if (open > cursor) parts.push({ type: 'text', text: value.slice(cursor, open) });
    const rawOrdinals = marker[1] ?? '';
    const ordinals = parseOrdinals(rawOrdinals);
    parts.push({
      type: 'shape',
      shape: makeShape(parsed.kind, ordinals[0] ?? 0, parsed.properties, body.slice(IMAGE_PREFIX.length), rawOrdinals),
    });
    cursor = close + 2;
  }
  return { parts };
}

/** Every shape of a document, in field order. */
export function documentShapes(document: OcclusionDocument): OcclusionShape[] {
  return document.parts.filter((part): part is { type: 'shape'; shape: OcclusionShape } => part.type === 'shape').map((part) => part.shape);
}

/** Reads one property as a number; a missing or malformed value falls back. */
function numberProperty(shape: OcclusionShape, name: string, fallback: number): number {
  const value = shape.properties.find((property) => property.name === name)?.value;
  if (value === undefined || value === '') return fallback;
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function stringProperty(shape: OcclusionShape, name: string): string | undefined {
  return shape.properties.find((property) => property.name === name)?.value;
}

function parsePoints(value: string | undefined): OcclusionPoint[] {
  if (!value) return [];
  const points: OcclusionPoint[] = [];
  for (const pair of value.split(' ')) {
    if (pair === '') continue;
    const [x, y] = pair.split(',');
    const pointX = Number.parseFloat(x ?? '');
    const pointY = Number.parseFloat(y ?? '');
    if (!Number.isFinite(pointX) || !Number.isFinite(pointY)) continue;
    points.push({ x: pointX, y: pointY });
  }
  return points;
}

/** Converts stored angle steps to degrees. */
export function storedAngleToDegrees(value: string | undefined): number {
  const steps = Number(value ?? 0) % ANGLE_STEPS;
  if (!Number.isFinite(steps)) return 0;
  return (steps / ANGLE_STEPS) * 360;
}

/** Converts degrees to the stored angle steps. */
export function degreesToStoredAngle(degrees: number): number {
  const normalized = ((degrees % 360) + 360) % 360;
  return Math.round((normalized / 360) * ANGLE_STEPS) % ANGLE_STEPS;
}

/** Reads the geometry a shape currently stores. */
export function shapeGeometry(shape: OcclusionShape): OcclusionGeometry {
  const geometry: OcclusionGeometry = {
    left: numberProperty(shape, 'left', 0),
    top: numberProperty(shape, 'top', 0),
    angle: storedAngleToDegrees(stringProperty(shape, 'angle')),
    fill: stringProperty(shape, 'fill'),
    occludeInactive: stringProperty(shape, OCClUDE_INACTIVE) === '1',
  };
  switch (shape.kind) {
    case 'rect':
      geometry.width = numberProperty(shape, 'width', 0);
      geometry.height = numberProperty(shape, 'height', 0);
      break;
    case 'ellipse':
      geometry.rx = numberProperty(shape, 'rx', 0);
      geometry.ry = numberProperty(shape, 'ry', 0);
      break;
    case 'polygon':
      geometry.points = parsePoints(stringProperty(shape, 'points'));
      break;
    case 'text':
      geometry.text = stringProperty(shape, 'text') ?? '';
      geometry.scale = numberProperty(shape, 'scale', 1);
      geometry.fontSize = numberProperty(shape, 'fs', 0);
      break;
  }
  return geometry;
}

/** Formats a coordinate without exponent notation and without a long float tail. */
export function formatCoordinate(value: number): string {
  if (!Number.isFinite(value)) return '0';
  const rounded = Math.round(value * 1e6) / 1e6;
  return Object.is(rounded, -0) ? '0' : String(rounded);
}

function geometryEntries(kind: OcclusionShapeKind, geometry: OcclusionGeometry): OcclusionProperty[] {
  const entries: OcclusionProperty[] = [
    { name: 'left', value: formatCoordinate(geometry.left) },
    { name: 'top', value: formatCoordinate(geometry.top) },
  ];
  switch (kind) {
    case 'rect':
      entries.push(
        { name: 'width', value: formatCoordinate(geometry.width ?? 0) },
        { name: 'height', value: formatCoordinate(geometry.height ?? 0) },
      );
      break;
    case 'ellipse':
      entries.push(
        { name: 'rx', value: formatCoordinate(geometry.rx ?? 0) },
        { name: 'ry', value: formatCoordinate(geometry.ry ?? 0) },
      );
      break;
    case 'polygon':
      entries.push({
        name: 'points',
        value: (geometry.points ?? []).map((point) => `${formatCoordinate(point.x)},${formatCoordinate(point.y)}`).join(' '),
      });
      break;
    case 'text':
      entries.push({ name: 'text', value: geometry.text ?? '' });
      entries.push({ name: 'scale', value: formatCoordinate(geometry.scale ?? 1) });
      if (geometry.fontSize !== undefined && geometry.fontSize > 0) {
        entries.push({ name: 'fs', value: formatCoordinate(geometry.fontSize) });
      }
      break;
  }
  if (geometry.angle !== undefined && geometry.angle !== 0) {
    entries.push({ name: 'angle', value: String(degreesToStoredAngle(geometry.angle)) });
  }
  if (geometry.fill !== undefined && geometry.fill !== '' && geometry.fill !== DEFAULT_FILL) {
    entries.push({ name: 'fill', value: geometry.fill });
  }
  if (geometry.occludeInactive) entries.push({ name: OCClUDE_INACTIVE, value: '1' });
  return entries;
}

/**
 * Writes new geometry into a shape, keeping the existing property order and any property this module does
 * not know about. That keeps an edit to one coordinate from rewriting the whole deletion.
 */
export function setShapeGeometry(shape: OcclusionShape, geometry: OcclusionGeometry): void {
  const entries = geometryEntries(shape.kind, geometry);
  const values = new Map(entries.map((entry) => [entry.name, entry.value]));
  // A zero angle is the default, so it is written by dropping the property rather than by storing "0".
  const dropAngle = (geometry.angle ?? 0) === 0;
  const seen = new Set<string>();
  const next: OcclusionProperty[] = [];
  for (const property of shape.properties) {
    if (property.name === 'angle' && dropAngle) continue;
    if (values.has(property.name)) {
      next.push({ name: property.name, value: values.get(property.name) ?? '' });
      seen.add(property.name);
    } else {
      next.push({ ...property });
    }
  }
  for (const entry of entries) {
    if (!seen.has(entry.name)) next.push({ ...entry });
  }
  shape.properties = next;
  shape.dirty = true;
}

/** Builds a clean new shape in the canonical property order. */
export function createShape(kind: OcclusionShapeKind, ordinal: number, geometry: OcclusionGeometry): OcclusionShape {
  return {
    id: newShapeId(),
    kind,
    ordinal,
    properties: geometryEntries(kind, geometry),
    dirty: true,
  };
}

/** Assigns a shape to a cloze number, marking it dirty only when the number actually changes. */
export function assignOrdinal(shape: OcclusionShape, ordinal: number): void {
  if (shape.ordinal === ordinal && !shape.rawOrdinals?.includes(',')) return;
  shape.ordinal = ordinal;
  shape.dirty = true;
}

/** Marks or unmarks a shape as an inactive mask of the "hide all, guess one" mode. */
export function setShapeOccludeInactive(shape: OcclusionShape, occludeInactive: boolean): void {
  const index = shape.properties.findIndex((property) => property.name === OCClUDE_INACTIVE);
  if (occludeInactive) {
    const existing = index >= 0 ? shape.properties[index] : undefined;
    if (existing) {
      if (existing.value === '1') return;
      existing.value = '1';
    } else {
      shape.properties.push({ name: OCClUDE_INACTIVE, value: '1' });
    }
  } else if (index >= 0) {
    shape.properties.splice(index, 1);
  } else {
    return;
  }
  shape.dirty = true;
}

/** The mode a document currently expresses: any `oi=1` means "hide all, guess one". */
export function documentMode(document: OcclusionDocument): OcclusionMode {
  return documentShapes(document).some((shape) => stringProperty(shape, OCClUDE_INACTIVE) === '1') ? 'hideAll' : 'hideOne';
}

/** Switches every shape of a document to the given mode. */
export function setDocumentMode(document: OcclusionDocument, mode: OcclusionMode): void {
  for (const shape of documentShapes(document)) setShapeOccludeInactive(shape, mode === 'hideAll');
}

function serializeProperties(properties: readonly OcclusionProperty[]): string {
  return properties.map((property) => `${property.name}=${encodePropertyValue(property.value)}`).join(':');
}

/** The body of one deletion, without the `image-occlusion:` prefix. */
export function serializeShapeBody(shape: OcclusionShape): string {
  return `${shape.kind}:${serializeProperties(shape.properties)}`;
}

/** Renders a document back to a field value. */
export function serializeOcclusionField(document: OcclusionDocument): string {
  let out = '';
  for (const part of document.parts) {
    if (part.type === 'text') {
      out += part.text;
      continue;
    }
    const shape = part.shape;
    if (!shape.dirty && shape.rawBody !== undefined && shape.rawOrdinals !== undefined) {
      out += `{{c${shape.rawOrdinals}::${IMAGE_PREFIX}${shape.rawBody}}}`;
    } else {
      out += `{{c${shape.ordinal}::${IMAGE_PREFIX}${serializeShapeBody(shape)}}}`;
    }
  }
  return out;
}

/** The first image URL a field value holds, or the empty string. Stored HTML is never injected into the page. */
export function firstImageSource(value: string): string {
  const match = /<img\b[^>]*?\bsrc\s*=\s*("([^"]*)"|'([^']*)')/i.exec(value);
  return match ? (match[2] ?? match[3] ?? '') : '';
}
