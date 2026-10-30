import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createRuntime, type FrameRuntime } from '../cardframe/runtime';
import {
  Ellipse,
  Polygon,
  Rectangle,
  Text,
  extractShapesFromRenderedClozes,
  shapesToPaint,
} from '../cardframe/image-occlusion';

/**
 * The review half of Anki's image occlusion, exercised against the markup the server produces.
 *
 * The server (internal/notetype/cloze.go) writes one `div` per shape and leaves the painting to the frame; the note type's
 * template calls `anki.imageOcclusion.setup()` from an inline script. happy-dom does not execute a dynamically inserted
 * script, so the cases below send the card through the runtime exactly as the review page does and then call the global
 * the way the template's script would.
 *
 * The fixtures are the M17-5 goldens, not a copy: `question_html` and `answer_html` are the server's own rendered output,
 * so a change in the markup reaches these cases without anyone retyping it here.
 */

interface GoldenNote {
  guid: string;
  notetype_name: string;
  fields: Record<string, string>;
  cards: Array<{ question_html: string; answer_html: string; question_css: string }>;
}

interface Golden {
  notes: GoldenNote[];
}

interface OcclusionFace {
  guid: string;
  notetype: string;
  side: 'question' | 'answer';
  html: string;
  css: string;
}

/**
 * The server's golden directory, reached from this file rather than imported: the JSON is data the Go tests own, and a
 * static import would make Vite copy every `.apkg` beside it into the test bundle.
 */
const GOLDEN_DIR = join(import.meta.dirname, '../../../internal/notetype/testdata/anki');

function golden(name: string): Golden {
  return JSON.parse(readFileSync(join(GOLDEN_DIR, name), 'utf8')) as Golden;
}

/** Every question and answer face of every note whose fields hold an image-occlusion shape. */
function occlusionFaces(name: string): OcclusionFace[] {
  const faces: OcclusionFace[] = [];
  for (const note of golden(name).notes) {
    const occluded = Object.values(note.fields).some((value) => value.includes('image-occlusion:'));
    if (!occluded) continue;
    for (const card of note.cards) {
      faces.push({
        guid: note.guid,
        notetype: note.notetype_name,
        side: 'question',
        html: card.question_html,
        css: card.question_css,
      });
      faces.push({
        guid: note.guid,
        notetype: note.notetype_name,
        side: 'answer',
        html: card.answer_html,
        css: card.question_css,
      });
    }
  }
  return faces;
}

interface CanvasCall {
  method: string;
  args: unknown[];
  fill: string;
  stroke: string;
}

function callsOf(calls: CanvasCall[], method: string): CanvasCall[] {
  return calls.filter((call) => call.method === method);
}

/**
 * A recording 2D context. happy-dom does not implement the canvas paint API, and the cases care about the shape list and
 * the coordinates it produces rather than pixels, so the fake records the calls the port makes.
 */
function recordingContext(): { calls: CanvasCall[]; context: CanvasRenderingContext2D } {
  const calls: CanvasCall[] = [];
  const state = { fillStyle: '', strokeStyle: '', lineWidth: 0, font: '', textBaseline: '' };
  const record = (method: string, ...args: unknown[]): void => {
    calls.push({ method, args, fill: state.fillStyle, stroke: state.strokeStyle });
  };
  const context = {
    get fillStyle() {
      return state.fillStyle;
    },
    set fillStyle(value: string) {
      state.fillStyle = value;
    },
    get strokeStyle() {
      return state.strokeStyle;
    },
    set strokeStyle(value: string) {
      state.strokeStyle = value;
    },
    get lineWidth() {
      return state.lineWidth;
    },
    set lineWidth(value: number) {
      state.lineWidth = value;
    },
    get font() {
      return state.font;
    },
    set font(value: string) {
      state.font = value;
    },
    get textBaseline() {
      return state.textBaseline;
    },
    set textBaseline(value: string) {
      state.textBaseline = value;
    },
    fillRect: (...args: number[]) => record('fillRect', ...args),
    strokeRect: (...args: number[]) => record('strokeRect', ...args),
    clearRect: (...args: number[]) => record('clearRect', ...args),
    ellipse: (...args: unknown[]) => record('ellipse', ...args),
    beginPath: () => record('beginPath'),
    closePath: () => record('closePath'),
    moveTo: (...args: number[]) => record('moveTo', ...args),
    lineTo: (...args: number[]) => record('lineTo', ...args),
    fill: () => record('fill'),
    stroke: () => record('stroke'),
    save: () => record('save'),
    restore: () => record('restore'),
    translate: (...args: number[]) => record('translate', ...args),
    rotate: (...args: number[]) => record('rotate', ...args),
    scale: (...args: number[]) => record('scale', ...args),
    fillText: (...args: unknown[]) => record('fillText', ...args),
    measureText: (text: string) => ({
      width: text.length * 10,
      actualBoundingBoxAscent: 8,
      actualBoundingBoxDescent: 2,
    }),
  };
  return { calls, context: context as unknown as CanvasRenderingContext2D };
}

interface FakeParent {
  postMessage: ReturnType<typeof vi.fn>;
}

let parent: FakeParent;
let runtime: FrameRuntime | null = null;
let calls: CanvasCall[] = [];

function fromParent(data: unknown): MessageEvent {
  return new MessageEvent('message', { data, source: parent as unknown as MessageEventSource });
}

function showMessage(face: OcclusionFace): MessageEvent {
  return fromParent({
    type: 'show',
    html: face.html,
    css: face.css,
    bodyClass: 'card',
    side: face.side,
    mediaBase: '/fm/token/',
    autoplay: false,
    ordinal: 0,
  });
}

/** Gives the image the size and loaded state a browser would report, then runs the template's setup call. */
async function renderFace(face: OcclusionFace, width = 300, height = 200): Promise<void> {
  const pending = runtime?.handleMessage(showMessage(face));
  if (pending) await pending;

  const image = document.querySelector<HTMLImageElement>('#image-occlusion-container img');
  if (image) {
    Object.defineProperty(image, 'naturalWidth', { configurable: true, value: width });
    Object.defineProperty(image, 'naturalHeight', { configurable: true, value: height });
    Object.defineProperty(image, 'complete', { configurable: true, value: true });
  }

  const anki = (window as unknown as { anki?: { imageOcclusion?: { setup(): Promise<void> } } }).anki;
  await anki?.imageOcclusion?.setup();
}

/** One occlusion card rendered by hand in the server's attribute format, for the modes the M17-5 fixtures do not cover. */
const HIDE_ALL_HTML =
  '<div id="err"></div>' +
  '<div id="image-occlusion-container"><img src="placeholder.png"><canvas id="image-occlusion-canvas"></canvas></div>' +
  '<div class="cloze" data-ordinal="1" data-shape="rect" data-left="0.1" data-top="0.2" data-width="0.3" data-height="0.4"></div>' +
  '<div class="cloze-inactive" data-ordinal="2" data-shape="ellipse" data-left="0.5" data-top="0.4" data-rx="0.1" data-ry="0.05" data-occludeInactive="1"></div>' +
  '<div class="cloze-inactive" data-ordinal="3" data-shape="polygon" data-left="0" data-top="0" data-points="0,0 0.5,0 0.5,0.5" data-occludeInactive="1"></div>' +
  '<div class="cloze-inactive" data-ordinal="4" data-shape="text" data-left="0.1" data-top="0.1" data-scale="1" data-font-size="0.2" data-text="Label" data-occludeInactive="1"></div>' +
  '<div><button id="toggle">Toggle Masks</button></div>';

const HIDE_ONE_HTML =
  '<div id="err"></div>' +
  '<div id="image-occlusion-container"><img src="placeholder.png"><canvas id="image-occlusion-canvas"></canvas></div>' +
  '<div class="cloze" data-ordinal="1" data-shape="rect" data-left="0.1" data-top="0.2" data-width="0.3" data-height="0.4"></div>' +
  '<div class="cloze-inactive" data-ordinal="2" data-shape="ellipse" data-left="0.5" data-top="0.4" data-rx="0.1" data-ry="0.05"></div>' +
  '<div><button id="toggle">Toggle Masks</button></div>';

function customFace(html: string): OcclusionFace {
  return { guid: 'custom', notetype: 'Cloze Image Occlusion', side: 'question', html, css: '' };
}

beforeEach(() => {
  parent = { postMessage: vi.fn() };
  calls = [];
  document.head.innerHTML = '';
  document.body.innerHTML = '<div id="qa"></div>';
  document.documentElement.className = '';
  delete (window as unknown as { MathJax?: unknown }).MathJax;
  delete (window as unknown as { onUpdateHook?: unknown }).onUpdateHook;
  delete (window as unknown as { onShownHook?: unknown }).onShownHook;

  const recording = recordingContext();
  calls = recording.calls;
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(recording.context);
});

afterEach(() => {
  runtime?.destroy();
  runtime = null;
  vi.restoreAllMocks();
  delete (window as unknown as { anki?: unknown }).anki;
});

describe('image occlusion faces from the M17-5 goldens', () => {
  it('draws the stock note question as one active rectangle and the answer as its highlight', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });

    const question = occlusionFaces('stock.golden.json').find((face) => face.side === 'question');
    expect(question, 'the stock golden holds an image-occlusion question').toBeTruthy();
    await renderFace(question!);

    // 300x200 image, normalized 0.1/0.2/0.3/0.4 => 30/40/90/80.
    const fills = callsOf(calls, 'fillRect');
    expect(fills).toHaveLength(1);
    expect(fills[0]?.args).toEqual([30, 40, 90, 80]);
    expect(fills[0]?.fill).toBe('#ff8e8e');
    expect(callsOf(calls, 'strokeRect')[0]?.args).toEqual([30, 40, 90, 80]);
    expect(callsOf(calls, 'strokeRect')[0]?.stroke).toBe('#212121');

    // The painted list, not just the canvas calls: one active mask, nothing else in this card.
    const painted = shapesToPaint(document);
    expect(painted.map((item) => item.role)).toEqual(['active']);
    expect(painted[0]?.shape).toBeInstanceOf(Rectangle);
    expect(painted[0]?.shape).toMatchObject({ left: 0.1, top: 0.2, width: 0.3, height: 0.4, occludeInactive: true });

    calls.length = 0;
    const answer = occlusionFaces('stock.golden.json').find((face) => face.side === 'answer');
    expect(answer, 'the stock golden holds an image-occlusion answer').toBeTruthy();
    await renderFace(answer!);

    const answerFills = callsOf(calls, 'fillRect');
    expect(answerFills).toHaveLength(1);
    expect(answerFills[0]?.args).toEqual([30, 40, 90, 80]);
    expect(answerFills[0]?.fill).toBe('#ff8e8e00');
    expect(callsOf(calls, 'strokeRect')[0]?.stroke).toBe('#ff8e8e');
    expect(shapesToPaint(document).map((item) => item.role)).toEqual(['highlight']);
  });

  it('reads the cloze golden shape list even when its template draws no canvas', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });

    const question = occlusionFaces('cloze.golden.json').find((face) => face.side === 'question');
    expect(question, 'the cloze golden holds an image-occlusion question').toBeTruthy();
    await renderFace(question!);

    // This note type's template is just `{{cloze:Occlusion}}`: there is no container, so setup draws nothing and does
    // not throw. The shape list is still the server's, which is what an editor or a future template would read.
    expect(document.querySelector('#image-occlusion-canvas')).toBeNull();
    expect(shapesToPaint(document).map((item) => item.role)).toEqual(['active']);
    expect(calls).toHaveLength(0);
  });

  it('draws every fixture answer with a highlight shape only', async () => {
    for (const face of occlusionFaces('cloze.golden.json').concat(occlusionFaces('stock.golden.json'))) {
      if (face.side !== 'answer') continue;
      runtime?.destroy();
      document.body.innerHTML = '<div id="qa"></div>';
      calls.length = 0;
      runtime = createRuntime({ parent: parent as unknown as Window });
      await renderFace(face);
      expect(shapesToPaint(document).map((item) => item.role), face.guid).toEqual(['highlight']);
    }
  });
});

describe('image occlusion modes and shapes', () => {
  it('masks every inactive shape carrying oi=1 and draws all four shape kinds', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await renderFace(customFace(HIDE_ALL_HTML));

    const painted = shapesToPaint(document);
    expect(painted.map((item) => item.role)).toEqual(['active', 'inactive', 'inactive', 'inactive']);
    expect(painted.map((item) => item.shape.constructor)).toEqual([Rectangle, Ellipse, Polygon, Text]);
    expect(painted.map((item) => item.shape.ordinal)).toEqual([1, 2, 3, 4]);

    // Rectangle: 30,40 90x80.
    expect(callsOf(calls, 'fillRect').map((call) => call.args)).toContainEqual([30, 40, 90, 80]);
    // Ellipse is stored by its top-left corner plus radii: center (180, 90), radii 30 and 10.
    expect(callsOf(calls, 'ellipse')[0]?.args.slice(0, 6)).toEqual([180, 90, 30, 10, 0, 0]);
    // Polygon vertices are normalized and converted per axis: (0,0), (150,0), (150,100).
    expect(callsOf(calls, 'moveTo')[0]?.args).toEqual([0, 0]);
    expect(callsOf(calls, 'lineTo').map((call) => call.args)).toEqual([
      [0, 0],
      [150, 0],
      [150, 100],
    ]);
    // Text: a 40px line, one line of 50px measured width, on a white padded rectangle.
    expect(callsOf(calls, 'fillText')[0]?.args).toEqual(['Label', 30, 20]);
    expect(callsOf(calls, 'fillRect').map((call) => call.args)).toContainEqual([30, 20, 55, 20]);
  });

  it('keeps the other shapes visible when oi is absent (hide one, guess one)', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await renderFace(customFace(HIDE_ONE_HTML));

    expect(shapesToPaint(document).map((item) => item.role)).toEqual(['active']);
    expect(callsOf(calls, 'fillRect')).toHaveLength(1);

    // No oi=1 shape means there is no mask to hide, so the template's button stays out of the face.
    const button = document.querySelector<HTMLButtonElement>('#toggle');
    expect(button?.style.display).toBe('none');
  });

  it('toggles masks with the button and the M key, keeping text labels', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await renderFace(customFace(HIDE_ALL_HTML));

    const button = document.querySelector<HTMLButtonElement>('#toggle');
    expect(button?.style.display).not.toBe('none');
    const before = shapesToPaint(document).length;

    calls.length = 0;
    button?.click();
    expect(callsOf(calls, 'clearRect')[0]?.args).toEqual([0, 0, 300, 200]);
    // Hiding the masks leaves only the text label: its white background and its glyphs.
    expect(callsOf(calls, 'fillText')).toHaveLength(1);
    expect(callsOf(calls, 'fillRect')).toHaveLength(1);

    calls.length = 0;
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'M' }));
    expect(shapesToPaint(document)).toHaveLength(before);
    expect(callsOf(calls, 'ellipse')).toHaveLength(1);
  });

  it('redraws at the new scale after a resize and reads angle and fill from the markup', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await renderFace(customFace(HIDE_ONE_HTML));

    calls.length = 0;
    const image = document.querySelector<HTMLImageElement>('#image-occlusion-container img');
    Object.defineProperty(image, 'naturalWidth', { configurable: true, value: 150 });
    Object.defineProperty(image, 'naturalHeight', { configurable: true, value: 100 });
    window.dispatchEvent(new Event('resize'));

    // 150x100 image: 0.1/0.2/0.3/0.4 => 15/20/45/40.
    expect(callsOf(calls, 'fillRect')[0]?.args).toEqual([15, 20, 45, 40]);

    const rotated = customFace(
      '<div id="image-occlusion-container"><img><canvas id="image-occlusion-canvas"></canvas></div>' +
        '<div class="cloze" data-ordinal="1" data-shape="rect" data-left="0.1" data-top="0.2" data-width="0.3" data-height="0.4" data-angle="2500" data-fill="#123456"></div>',
    );
    await renderFace(rotated);
    expect(callsOf(calls, 'rotate')[0]?.args).toEqual([Math.PI / 2]);
    expect(callsOf(calls, 'fillRect').at(-1)?.fill).toBe('#ff8e8e');
  });
});

describe('image occlusion globals', () => {
  it('installs the modern and deprecated setup names as the same function', () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    const anki = (window as unknown as {
      anki?: { imageOcclusion?: { setup?: unknown }; setupImageCloze?: unknown };
    }).anki;
    expect(typeof anki?.imageOcclusion?.setup).toBe('function');
    expect(anki?.setupImageCloze).toBe(anki?.imageOcclusion?.setup);
  });

  it('reads the server attribute spelling for occlusion flags and text escapes', () => {
    document.body.innerHTML =
      '<div class="cloze-inactive" data-shape="text" data-left="0.1" data-top="0.2" data-scale="1" data-font-size="0.1" data-text="a:b" data-occludeInactive="1"></div>' +
      '<div class="cloze-inactive" data-shape="ellipse" data-left="0.1" data-top="0.2" data-rx="0.1" data-ry="0.2" data-occludeInactive="0"></div>';
    const shapes = extractShapesFromRenderedClozes(document, '.cloze-inactive');
    expect(shapes).toHaveLength(2);
    expect(shapes[0]).toBeInstanceOf(Text);
    expect(shapes[0]).toMatchObject({ occludeInactive: true, text: 'a:b' });
    expect(shapes[1]).toMatchObject({ occludeInactive: false });
  });
});
