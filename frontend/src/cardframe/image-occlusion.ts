// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
//
// Ported from the review half of ts/routes/image-occlusion/review.ts at tag 26.09.3 of
// ankitects/anki, together with the shape extraction of shapes/from-cloze.ts, the shape
// model of shapes/{base,rectangle,ellipse,polygon,text}.ts and the canvas pixel sizing of
// canvas-scale.ts at the same tag.
//
// Only the reviewer path is ported. The mask editor's fabric-based drawing, editing and
// serialisation stay out: card rendering needs to read the shapes the server wrote into
// the face and paint them, never to create them.
//
// The server emits one `div` per occlusion shape (internal/notetype/cloze.go), carrying
// normalized coordinates as data attributes and one of the classes `cloze`,
// `cloze-inactive` or `cloze-highlight`. An active shape is hidden on the question and
// highlighted on the answer; an inactive shape is masked only when it carries
// `data-occludeInactive="1"`, which is how the note encodes "hide all, guess one". Without
// that attribute the note is in "hide one, guess one" mode and the other shapes stay
// visible.

/** The fill of a mask, as Anki's image-occlusion note type declares it. */
export const SHAPE_MASK_COLOR = '#ffeba2';
/** Text labels are drawn on this background so they stay readable over the image. */
export const TEXT_BACKGROUND_COLOR = '#ffffff';
export const TEXT_FONT_FAMILY = 'Arial';
export const TEXT_PADDING = 5;
/** Font size used when a text shape stores none, in canvas pixels. */
export const TEXT_FONT_SIZE = 40;
export const TEXT_COLOR = '#000000';

/** Anki's default mask colours, used when the note type's CSS does not define them. */
const DEFAULT_PROPERTIES: ShapeProperties = {
  activeShapeColor: '#ff8e8e',
  inActiveShapeColor: SHAPE_MASK_COLOR,
  highlightShapeColor: '#ff8e8e00',
  activeBorder: { width: 1, color: '#212121' },
  inActiveBorder: { width: 1, color: '#212121' },
  highlightShapeBorder: { width: 1, color: '#ff8e8e' },
};

const ANGLE_STEPS = 10000;

/** A canvas backing-store size in device pixels. */
export interface Size {
  width: number;
  height: number;
}

export interface ShapeInit {
  left?: number;
  top?: number;
  angle?: number;
  fill?: string;
  /** Whether the shape is a mask of the current mode's non-guessed shapes. */
  occludeInactive?: boolean;
  ordinal?: number;
}

/**
 * Converts stored angle steps to degrees. The note stores an angle as a fraction of a
 * full turn, so the value survives the round trip through a text field without depending
 * on float formatting.
 */
export function storedToAngle(value: string | undefined): number {
  const steps = Number(value ?? 0) % ANGLE_STEPS;
  if (!Number.isFinite(steps)) return 0;
  return (steps / ANGLE_STEPS) * 360;
}

/**
 * A shape with coordinates normalized against the image (0–1). The stored form is
 * normalized so a mask stays on the same part of the picture at any display size; the
 * drawing step converts to canvas pixels with `toAbsolute`.
 */
export class Shape {
  left: number;
  top: number;
  angle: number;
  fill: string;
  occludeInactive: boolean;
  ordinal: number;

  constructor(init: ShapeInit = {}) {
    this.left = init.left ?? 0;
    this.top = init.top ?? 0;
    this.angle = init.angle ?? 0;
    this.fill = init.fill ?? SHAPE_MASK_COLOR;
    this.occludeInactive = init.occludeInactive ?? false;
    this.ordinal = init.ordinal ?? 0;
  }

  absolutePosition(size: Size): { left: number; top: number } {
    return { left: Math.round(this.left * size.width), top: Math.round(this.top * size.height) };
  }

  toAbsolute(size: Size): Shape {
    return new Shape({ ...this, ...this.absolutePosition(size) });
  }
}

export interface RectangleInit extends ShapeInit {
  width?: number;
  height?: number;
}

export class Rectangle extends Shape {
  width: number;
  height: number;

  constructor(init: RectangleInit = {}) {
    const { width = 0, height = 0, ...rest } = init;
    super(rest);
    this.width = width;
    this.height = height;
  }

  toAbsolute(size: Size): Rectangle {
    return new Rectangle({
      ...this,
      ...this.absolutePosition(size),
      width: Math.round(this.width * size.width),
      height: Math.round(this.height * size.height),
    });
  }
}

export interface EllipseInit extends ShapeInit {
  rx?: number;
  ry?: number;
}

export class Ellipse extends Shape {
  rx: number;
  ry: number;

  constructor(init: EllipseInit = {}) {
    const { rx = 0, ry = 0, ...rest } = init;
    super(rest);
    this.rx = rx;
    this.ry = ry;
  }

  toAbsolute(size: Size): Ellipse {
    return new Ellipse({
      ...this,
      ...this.absolutePosition(size),
      rx: Math.round(this.rx * size.width),
      ry: Math.round(this.ry * size.height),
    });
  }
}

export interface PointInit {
  x?: number;
  y?: number;
}

/** One polygon vertex, normalized like the other shape coordinates. */
export class Point {
  x: number;
  y: number;

  constructor(init: PointInit = {}) {
    this.x = init.x ?? 0;
    this.y = init.y ?? 0;
  }
}

export interface PolygonInit extends ShapeInit {
  points?: Point[];
}

export class Polygon extends Shape {
  points: Point[];

  constructor(init: PolygonInit = {}) {
    const { points = [], ...rest } = init;
    super(rest);
    this.points = points;
  }

  toAbsolute(size: Size): Polygon {
    return new Polygon({
      ...this,
      ...this.absolutePosition(size),
      points: this.points.map((point) => new Point({ x: Math.round(point.x * size.width), y: Math.round(point.y * size.height) })),
    });
  }
}

export interface TextInit extends ShapeInit {
  text?: string;
  scaleX?: number;
  scaleY?: number;
  fontSize?: number;
}

/** A text label. Its `fontSize` is normalized against the image height like the other coordinates. */
export class Text extends Shape {
  text: string;
  scaleX: number;
  scaleY: number;
  fontSize: number | undefined;

  constructor(init: TextInit = {}) {
    const { text = '', scaleX = 1, scaleY = 1, fontSize, fill = TEXT_COLOR, ...rest } = init;
    super({ ...rest, fill });
    this.text = text;
    this.scaleX = scaleX;
    this.scaleY = scaleY;
    this.fontSize = fontSize;
  }

  toAbsolute(size: Size): Text {
    return new Text({
      ...this,
      ...this.absolutePosition(size),
      fontSize: this.fontSize ? this.fontSize * size.height : TEXT_FONT_SIZE,
    });
  }
}

/** The shape kinds the server writes into `data-shape`. */
export type ShapeType = 'rect' | 'ellipse' | 'polygon' | 'text';

function isValidShapeType(type: string | undefined): type is ShapeType {
  return type === 'rect' || type === 'ellipse' || type === 'polygon' || type === 'text';
}

/**
 * Reads a numeric data attribute. A missing or malformed value falls back instead of
 * producing NaN: the server omits zero-sized attributes, and one NaN coordinate would
 * poison every later canvas call for the rest of the face.
 */
function parseNumber(value: string | undefined, fallback: number): number {
  if (value === undefined || value === '') return fallback;
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function parsePoints(value: string | undefined): Point[] {
  if (value === undefined || value === '') return [new Point({ x: 0, y: 0 })];
  const points = value
    .split(' ')
    .filter((pair) => pair !== '')
    .map((pair) => {
      const [x, y] = pair.split(',');
      return new Point({ x: parseNumber(x, 0), y: parseNumber(y, 0) });
    });
  return points.length > 0 ? points : [new Point({ x: 0, y: 0 })];
}

/**
 * Builds one shape from a rendered `.cloze*` div. The attribute spelling is the server's
 * contract: `data-shape` names the kind, and the remaining values are the normalized
 * fields of the shape, with `data-occludeInactive` and `data-font-size` written in the
 * camel-case spelling that the HTML parser lowercases.
 */
function shapeFromElement(element: HTMLElement): Shape | null {
  const type = element.dataset.shape;
  if (!isValidShapeType(type)) return null;

  const data = element.dataset;
  const common: ShapeInit = {
    left: parseNumber(data.left, 0),
    top: parseNumber(data.top, 0),
    angle: storedToAngle(data.angle),
    occludeInactive: data.occludeinactive === '1',
    ordinal: parseNumber(data.ordinal, 0),
    ...(data.fill === undefined ? {} : { fill: data.fill }),
  };

  switch (type) {
    case 'rect':
      return new Rectangle({ ...common, width: parseNumber(data.width, 0), height: parseNumber(data.height, 0) });
    case 'ellipse':
      return new Ellipse({ ...common, rx: parseNumber(data.rx, 0), ry: parseNumber(data.ry, 0) });
    case 'polygon':
      return new Polygon({ ...common, points: parsePoints(data.points) });
    case 'text':
      return new Text({
        ...common,
        text: data.text ?? '',
        scaleX: parseNumber(data.scale, 1),
        scaleY: parseNumber(data.scale, 1),
        ...(data.fontSize === undefined ? {} : { fontSize: parseNumber(data.fontSize, TEXT_FONT_SIZE) }),
      });
  }
}

/** Collects the shapes rendered under `root` that match one of the three cloze classes. */
export function extractShapesFromRenderedClozes(root: ParentNode, selector: string): Shape[] {
  const shapes: Shape[] = [];
  for (const node of Array.from(root.querySelectorAll(selector))) {
    if (!(node instanceof HTMLElement)) continue;
    const shape = shapeFromElement(node);
    if (shape) shapes.push(shape);
  }
  return shapes;
}

/** Which mask colour a shape is painted with on the current face. */
export type PaintedRole = 'active' | 'inactive' | 'highlight';

export interface PaintedShape {
  shape: Shape;
  role: PaintedRole;
}

/**
 * shapesToPaint answers which shapes the canvas draws, in Anki's order: the active
 * guesses first, then the masks of the other shapes, then the answer highlight. A shape
 * outside `allowedShapes` is dropped, which is how the mask toggle keeps text labels when
 * it hides every mask.
 */
export function shapesToPaint(
  root: ParentNode,
  allowedShapes?: Array<typeof Shape>,
): PaintedShape[] {
  const allowed = (shape: Shape): boolean =>
    !allowedShapes || allowedShapes.length === 0 || allowedShapes.some((kind) => shape instanceof kind);

  const painted: PaintedShape[] = [];
  for (const shape of extractShapesFromRenderedClozes(root, '.cloze')) {
    if (allowed(shape)) painted.push({ shape, role: 'active' });
  }
  for (const shape of extractShapesFromRenderedClozes(root, '.cloze-inactive')) {
    // `oi=1` is the note's "hide all, guess one" flag: only those inactive shapes are
    // masked. Without it the mode is "hide one, guess one" and they stay visible.
    if (shape.occludeInactive && allowed(shape)) painted.push({ shape, role: 'inactive' });
  }
  for (const shape of extractShapesFromRenderedClozes(root, '.cloze-highlight')) {
    if (allowed(shape)) painted.push({ shape, role: 'highlight' });
  }
  return painted;
}

export interface BorderStyle {
  width: number;
  color: string;
}

export interface ShapeProperties {
  activeShapeColor: string;
  inActiveShapeColor: string;
  highlightShapeColor: string;
  activeBorder: BorderStyle;
  inActiveBorder: BorderStyle;
  highlightShapeBorder: BorderStyle;
}

function parseBorder(value: string, fallback: BorderStyle): BorderStyle {
  const parts = value.split(' ').filter((part) => part !== '');
  const width = Number.parseFloat(parts[0] ?? '');
  return {
    width: Number.isFinite(width) ? width : fallback.width,
    color: parts[1] ?? fallback.color,
  };
}

/**
 * Reads the mask colours from the canvas's computed style. The note type declares them as
 * custom properties on `#image-occlusion-canvas`; a stylesheet that omits one falls back
 * to Anki's default, because a missing custom property returns an empty string rather
 * than throwing.
 */
export function getShapeProperties(win: Window, doc: Document): ShapeProperties {
  const canvas = doc.getElementById('image-occlusion-canvas');
  if (!canvas || typeof win.getComputedStyle !== 'function') return { ...DEFAULT_PROPERTIES };
  const style = win.getComputedStyle(canvas);
  const read = (name: string): string => style.getPropertyValue(name).trim();
  const activeShapeColor = read('--active-shape-color');
  const inActiveShapeColor = read('--inactive-shape-color');
  const highlightShapeColor = read('--highlight-shape-color');
  return {
    activeShapeColor: activeShapeColor || DEFAULT_PROPERTIES.activeShapeColor,
    inActiveShapeColor: inActiveShapeColor || DEFAULT_PROPERTIES.inActiveShapeColor,
    highlightShapeColor: highlightShapeColor || DEFAULT_PROPERTIES.highlightShapeColor,
    activeBorder: parseBorder(read('--active-shape-border'), DEFAULT_PROPERTIES.activeBorder),
    inActiveBorder: parseBorder(read('--inactive-shape-border'), DEFAULT_PROPERTIES.inActiveBorder),
    highlightShapeBorder: parseBorder(read('--highlight-shape-border'), DEFAULT_PROPERTIES.highlightShapeBorder),
  };
}

export interface DrawShapesData {
  activeShapes: Shape[];
  inactiveShapes: Shape[];
  highlightShapes: Shape[];
  properties: ShapeProperties;
}

export type DrawShapesFilter = (data: DrawShapesData, context: CanvasRenderingContext2D) => DrawShapesData | void;
export type DrawShapesCallback = (data: DrawShapesData, context: CanvasRenderingContext2D) => void;

export interface SetupImageOcclusionOptions {
  onWillDrawShapes?: DrawShapesFilter;
  onDidDrawShapes?: DrawShapesCallback;
}

export interface DrawShapeParameters {
  context: CanvasRenderingContext2D;
  size: Size;
  shape: Shape;
  fill: string;
  stroke: string;
  strokeWidth: number;
}

function topLeftOfPoints(points: Point[]): { x: number; y: number } {
  const first = points[0] ?? new Point();
  let top = first.y;
  let left = first.x;
  for (const point of points) {
    if (point.y < top) top = point.y;
    if (point.x < left) left = point.x;
  }
  return { x: left, y: top };
}

function getPolygonOffset(polygon: Polygon): { x: number; y: number } {
  const topLeft = topLeftOfPoints(polygon.points);
  return { x: polygon.left - topLeft.x, y: polygon.top - topLeft.y };
}

/**
 * Paints one shape. Coordinates arrive normalized and are converted against the canvas
 * size first, so the drawing code works in pixels only.
 */
export function drawShape({ context, size, shape, fill, stroke, strokeWidth }: DrawShapeParameters): void {
  const absolute = shape.toAbsolute(size);
  context.fillStyle = fill;
  context.strokeStyle = stroke;
  context.lineWidth = strokeWidth;
  const angle = ((absolute.angle ?? 0) * Math.PI) / 180;

  if (absolute instanceof Rectangle) {
    if (angle) {
      context.save();
      context.translate(absolute.left, absolute.top);
      context.rotate(angle);
      context.translate(-absolute.left, -absolute.top);
    }
    context.fillRect(absolute.left, absolute.top, absolute.width, absolute.height);
    // A zero width would still paint a visible hairline, so the stroke is conditional.
    if (strokeWidth) context.strokeRect(absolute.left, absolute.top, absolute.width, absolute.height);
    if (angle) context.restore();
    return;
  }

  if (absolute instanceof Ellipse) {
    const centerX = absolute.left + absolute.rx;
    const centerY = absolute.top + absolute.ry;
    if (angle) {
      context.save();
      context.translate(absolute.left, absolute.top);
      context.rotate(angle);
      context.translate(-absolute.left, -absolute.top);
    }
    context.beginPath();
    context.ellipse(centerX, centerY, absolute.rx, absolute.ry, 0, 0, Math.PI * 2, false);
    context.closePath();
    context.fill();
    if (strokeWidth) context.stroke();
    if (angle) context.restore();
    return;
  }

  if (absolute instanceof Polygon) {
    const first = absolute.points[0];
    if (!first) return;
    const offset = getPolygonOffset(absolute);
    context.save();
    context.translate(offset.x, offset.y);
    context.beginPath();
    context.moveTo(first.x, first.y);
    for (const point of absolute.points) context.lineTo(point.x, point.y);
    context.closePath();
    context.fill();
    if (strokeWidth) context.stroke();
    context.restore();
    return;
  }

  if (absolute instanceof Text) {
    const fontSize = absolute.fontSize ?? TEXT_FONT_SIZE;
    context.save();
    context.font = `${fontSize}px ${TEXT_FONT_FAMILY}`;
    context.textBaseline = 'top';
    context.scale(absolute.scaleX, absolute.scaleY);
    const scaleX = absolute.scaleX || 1;
    const scaleY = absolute.scaleY || 1;
    const lines = absolute.text.split('\n');
    const baseMetrics = context.measureText('M');
    // A canvas implementation may report no glyph metrics; the font size is then a safe
    // stand-in for the line box, because a missing metric must not turn the coordinates
    // into NaN and blank the whole canvas.
    const ascent = baseMetrics.actualBoundingBoxAscent;
    const descent = baseMetrics.actualBoundingBoxDescent;
    const fontHeight = Number.isFinite(ascent + descent) && ascent + descent > 0 ? ascent + descent : fontSize;
    const lineHeight = 1.5 * fontHeight;
    const left = absolute.left / scaleX;
    const top = absolute.top / scaleY;
    let maxWidth = 0;
    let totalHeight = 0;
    const positions: Array<{ text: string; x: number; y: number }> = [];
    lines.forEach((line, index) => {
      const metrics = context.measureText(line);
      positions.push({ text: line, x: left, y: top + index * lineHeight });
      if (metrics.width > maxWidth) maxWidth = metrics.width;
      totalHeight += lineHeight;
    });
    if (angle) {
      context.translate(left, top);
      context.rotate(angle);
      context.translate(-left, -top);
    }
    context.fillStyle = TEXT_BACKGROUND_COLOR;
    context.fillRect(left, top, maxWidth + TEXT_PADDING, totalHeight + TEXT_PADDING);
    context.fillStyle = absolute.fill;
    for (const position of positions) context.fillText(position.text, position.x, position.y);
    context.restore();
  }
}

/**
 * Paints every shape the face draws, in Anki's order, and lets the caller inspect or
 * replace the shape lists before and after the paint.
 */
export function drawShapes(
  win: Window,
  doc: Document,
  canvas: HTMLCanvasElement,
  options?: SetupImageOcclusionOptions,
  allowedShapes?: Array<typeof Shape>,
): void {
  const context = canvas.getContext('2d');
  if (!context) return;

  let shapes = shapesToPaint(doc, allowedShapes);
  let properties = getShapeProperties(win, doc);

  if (options?.onWillDrawShapes) {
    const processed = options.onWillDrawShapes(
      {
        activeShapes: shapes.filter((item) => item.role === 'active').map((item) => item.shape),
        inactiveShapes: shapes.filter((item) => item.role === 'inactive').map((item) => item.shape),
        highlightShapes: shapes.filter((item) => item.role === 'highlight').map((item) => item.shape),
        properties,
      },
      context,
    );
    if (processed) {
      properties = processed.properties;
      shapes = [
        ...processed.activeShapes.map((shape) => ({ shape, role: 'active' as const })),
        ...processed.inactiveShapes.map((shape) => ({ shape, role: 'inactive' as const })),
        ...processed.highlightShapes.map((shape) => ({ shape, role: 'highlight' as const })),
      ];
    }
  }

  for (const { shape, role } of shapes) {
    if (role === 'active') {
      drawShape({
        context,
        size: canvas,
        shape,
        fill: properties.activeShapeColor,
        stroke: properties.activeBorder.color,
        strokeWidth: properties.activeBorder.width,
      });
    } else if (role === 'inactive') {
      drawShape({
        context,
        size: canvas,
        shape,
        fill: shape.fill !== SHAPE_MASK_COLOR ? shape.fill : properties.inActiveShapeColor,
        stroke: properties.inActiveBorder.color,
        strokeWidth: properties.inActiveBorder.width,
      });
    } else {
      drawShape({
        context,
        size: canvas,
        shape,
        fill: properties.highlightShapeColor,
        stroke: properties.highlightShapeBorder.color,
        strokeWidth: properties.highlightShapeBorder.width,
      });
    }
  }

  options?.onDidDrawShapes?.(
    {
      activeShapes: shapes.filter((item) => item.role === 'active').map((item) => item.shape),
      inactiveShapes: shapes.filter((item) => item.role === 'inactive').map((item) => item.shape),
      highlightShapes: shapes.filter((item) => item.role === 'highlight').map((item) => item.shape),
      properties,
    },
    context,
  );
}

/**
 * Chooses the canvas backing-store size for the displayed container, in device pixels.
 * Scaling by the device pixel ratio keeps masks sharp on a high-density display, and the
 * 4096x4096 cap keeps the canvas inside the size Safari accepts.
 */
export function optimumPixelSizeForCanvas(imageSize: Size, containerSize: Size, pixelScale: number): Size {
  let width = imageSize.width;
  let height = imageSize.height;
  const containerWidth = containerSize.width * pixelScale;
  const containerHeight = containerSize.height * pixelScale;
  const containerScale = Math.min(containerWidth / imageSize.width, containerHeight / imageSize.height);
  width *= containerScale;
  height *= containerScale;

  const maximumPixels = 4096 * 4096;
  const requiredPixels = width * height;
  if (requiredPixels > maximumPixels) {
    const shrinkScale = Math.sqrt(maximumPixels) / Math.sqrt(requiredPixels);
    width *= shrinkScale;
    height *= shrinkScale;
  }

  return { width: Math.max(0, Math.floor(width)), height: Math.max(0, Math.floor(height)) };
}

interface OcclusionView {
  canvas: HTMLCanvasElement;
  container: HTMLElement;
  image: HTMLImageElement | null;
}

interface Installation {
  win: Window;
  doc: Document;
  /** The toggle state of each canvas; a new face gets a new canvas and starts unmasked. */
  hidden: WeakMap<HTMLCanvasElement, boolean>;
  /** Setup options of the latest call per canvas, so the toggle repaints with the same hooks. */
  options: WeakMap<HTMLCanvasElement, SetupImageOcclusionOptions | undefined>;
  /** Canvases whose toggle button and keyboard shortcut are already wired. */
  wired: WeakSet<HTMLCanvasElement>;
}

function findView(canvas: HTMLCanvasElement): OcclusionView | null {
  const container = canvas.closest<HTMLElement>('#image-occlusion-container');
  if (!container) return null;
  return {
    canvas,
    container,
    image: container.querySelector<HTMLImageElement>('img'),
  };
}

function viewFor(view: OcclusionView): OcclusionView | null {
  if (!view.canvas.isConnected) return null;
  return view;
}

/** Sizes the canvas for the current layout and paints the shapes of the current face. */
function render(installation: Installation, view: OcclusionView): void {
  const { win, doc } = installation;
  const { canvas, container, image } = view;
  const imageWidth = image?.naturalWidth ?? 0;
  const imageHeight = image?.naturalHeight ?? 0;

  if (imageWidth > 0 && imageHeight > 0) {
    // The image sits absolutely over the container so the canvas and the picture share
    // one coordinate space; the aspect ratio gives the container the image's height from
    // its width.
    container.style.aspectRatio = `${imageWidth} / ${imageHeight}`;
  }

  const containerSize = { width: canvas.clientWidth, height: canvas.clientHeight };
  if (imageWidth > 0 && imageHeight > 0 && containerSize.width > 0 && containerSize.height > 0) {
    const pixelScale = win.devicePixelRatio > 0 ? win.devicePixelRatio : 1;
    const size = optimumPixelSizeForCanvas(
      { width: imageWidth, height: imageHeight },
      containerSize,
      pixelScale,
    );
    canvas.width = size.width;
    canvas.height = size.height;
  } else {
    // A canvas without layout (the frame's document before it is measured, and the
    // test environment) still uses the image's own pixel size, so the masks are drawn at
    // the coordinates the markup describes instead of collapsing into a 0x0 bitmap.
    canvas.width = Math.max(0, Math.round(imageWidth));
    canvas.height = Math.max(0, Math.round(imageHeight));
  }

  wireToggle(installation, view);
  drawShapes(
    win,
    doc,
    canvas,
    installation.options.get(canvas),
    installation.hidden.get(canvas) ? [Text] : undefined,
  );
}

function toggleMasks(installation: Installation, view: OcclusionView): void {
  const context = view.canvas.getContext('2d');
  if (!context) return;
  const hide = !(installation.hidden.get(view.canvas) ?? false);
  installation.hidden.set(view.canvas, hide);
  context.clearRect(0, 0, view.canvas.width, view.canvas.height);
  // Hiding the masks keeps text labels: the label names the hidden structure, which is
  // the point of the button.
  drawShapes(
    installation.win,
    installation.doc,
    view.canvas,
    installation.options.get(view.canvas),
    hide ? [Text] : undefined,
  );
}

/**
 * Hooks up `#toggle` and the `M` key once per canvas. The button is hidden when the note
 * carries no `oi=1` shape, because then there is no mask to toggle off.
 */
function wireToggle(installation: Installation, view: OcclusionView): void {
  const { doc } = installation;
  const { canvas } = view;
  if (installation.wired.has(canvas)) return;
  installation.wired.add(canvas);

  const button = doc.getElementById('toggle');
  if (!button) return;
  if (doc.querySelector('[data-occludeinactive="1"]')) {
    button.addEventListener('click', () => {
      const current = findView(canvas);
      if (current) toggleMasks(installation, current);
    });
  } else {
    button.style.display = 'none';
  }
}

/**
 * Draws the occlusion of the face currently in the document. The template calls this
 * from its inline script; the deprecated `anki.setupImageCloze` name resolves to the same
 * function, because notes written for older Anki versions call it.
 *
 * The paint happens immediately, so a face whose image is already loaded is complete when
 * the script returns. An image that is still loading is painted once more from its `load`
 * handler, which is where the natural size becomes known.
 */
function setupImageOcclusion(
  installation: Installation,
  options?: SetupImageOcclusionOptions,
): Promise<void> {
  const canvas = installation.doc.querySelector<HTMLCanvasElement>('#image-occlusion-canvas');
  if (!canvas) return Promise.resolve();
  const view = findView(canvas);
  if (!view) return Promise.resolve();

  installation.options.set(canvas, options);
  render(installation, view);

  const image = view.image;
  if (image && !image.complete) {
    const redraw = (): void => {
      const current = viewFor(view);
      if (current) render(installation, current);
    };
    image.addEventListener('load', redraw, { once: true });
    image.addEventListener('error', redraw, { once: true });
  }
  return Promise.resolve();
}

interface ImageOcclusionApi {
  setup: (options?: SetupImageOcclusionOptions) => Promise<void>;
  drawShape: typeof drawShape;
  Ellipse: typeof Ellipse;
  Polygon: typeof Polygon;
  Rectangle: typeof Rectangle;
  Shape: typeof Shape;
  Text: typeof Text;
}

interface AnkiGlobal {
  imageOcclusion?: ImageOcclusionApi;
  setupImageCloze?: (options?: SetupImageOcclusionOptions) => Promise<void>;
}

interface WindowWithAnki extends Window {
  anki?: AnkiGlobal;
}

/**
 * Installs the reviewer's `anki.imageOcclusion` surface on the frame window and returns a
 * disposer. The runtime installs it once, before any card script runs, because the
 * template calls it from an inline script.
 */
export function installImageOcclusion(win: Window, doc: Document): () => void {
  const installation: Installation = {
    win,
    doc,
    hidden: new WeakMap(),
    options: new WeakMap(),
    wired: new WeakSet(),
  };

  const setup = (options?: SetupImageOcclusionOptions): Promise<void> => setupImageOcclusion(installation, options);
  const api: ImageOcclusionApi = {
    setup,
    drawShape,
    Ellipse,
    Polygon,
    Rectangle,
    Shape,
    Text,
  };

  const target = win as WindowWithAnki;
  target.anki = target.anki ?? {};
  target.anki.imageOcclusion = api;
  // The deprecated name is what a note written against an older Anki calls; it must stay
  // an alias of the same function for those notes to keep rendering.
  target.anki.setupImageCloze = setup;

  const onResize = (): void => {
    for (const canvas of Array.from(doc.querySelectorAll<HTMLCanvasElement>('#image-occlusion-canvas'))) {
      const view = findView(canvas);
      if (view && viewFor(view)) render(installation, view);
    }
  };

  const onKeyDown = (event: KeyboardEvent): void => {
    if (event.key !== 'M') return;
    const button = doc.getElementById('toggle');
    if (!button || button.style.display === 'none') return;
    const canvas = doc.querySelector<HTMLCanvasElement>('#image-occlusion-canvas');
    if (!canvas) return;
    const view = findView(canvas);
    if (view) toggleMasks(installation, view);
  };

  win.addEventListener('resize', onResize);
  doc.addEventListener('keydown', onKeyDown);

  return () => {
    win.removeEventListener('resize', onResize);
    doc.removeEventListener('keydown', onKeyDown);
  };
}
