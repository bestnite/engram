import { vi } from 'vitest';
import { flushSync } from 'svelte';

/**
 * Test helper for the card frames a view renders.
 *
 * A frame only shows its face after it posts `ready`, and in happy-dom the frame document never loads. The helper therefore
 * does what the frame page does: it records the parent's `show` messages by spying on the iframe's `contentWindow.postMessage`,
 * then delivers one `ready` from that same window and returns the face the component sent.
 *
 * One recorder per iframe element, so asking twice returns the same recording instead of replaying `ready`.
 */

export interface ShownFace {
  html: string;
  css: string;
  bodyClass: string;
  side: string;
  mediaBase: string;
  autoplay: boolean;
  ordinal: number;
}

interface Recorder {
  win: Window;
  shows: ShownFace[];
  ready: boolean;
}

const recorders = new WeakMap<HTMLIFrameElement, Recorder>();

function recorderFor(frame: HTMLIFrameElement): Recorder {
  const existing = recorders.get(frame);
  if (existing) return existing;
  const win = frame.contentWindow;
  if (!win) throw new Error('card frame has no contentWindow');
  const recorder: Recorder = { win, shows: [], ready: false };
  vi.spyOn(win, 'postMessage').mockImplementation((message: unknown) => {
    if (message && typeof message === 'object' && (message as { type?: unknown }).type === 'show') {
      recorder.shows.push(message as ShownFace);
    }
  });
  recorders.set(frame, recorder);
  return recorder;
}

/** Every card frame below `root`, in document order. */
export function frames(root: ParentNode): HTMLIFrameElement[] {
  return Array.from(root.querySelectorAll<HTMLIFrameElement>('iframe[data-card-frame]'));
}

async function drain(): Promise<void> {
  for (let i = 0; i < 5; i += 1) await Promise.resolve();
  flushSync();
  for (let i = 0; i < 5; i += 1) await Promise.resolve();
}

/** The latest face every frame was given, in document order. */
export async function shownFaces(root: ParentNode): Promise<ShownFace[]> {
  const list = frames(root);
  for (const frame of list) {
    const recorder = recorderFor(frame);
    if (recorder.ready) continue;
    recorder.ready = true;
    window.dispatchEvent(new MessageEvent('message', { data: { type: 'ready' }, source: recorder.win }));
  }
  await drain();
  return list.map((frame) => {
    const recorder = recorderFor(frame);
    const face = recorder.shows[recorder.shows.length - 1];
    if (!face) throw new Error('card frame never received a show message');
    return face;
  });
}

/** The latest HTML every frame was given, in document order. */
export async function shownHtml(root: ParentNode): Promise<string[]> {
  return (await shownFaces(root)).map((face) => face.html);
}

/** The latest HTML of the frames of one side (`question` or `answer`), in document order. */
export async function shownHtmlOnSide(root: ParentNode, side: string): Promise<string[]> {
  const faces = await shownFaces(root);
  return frames(root).flatMap((frame, index) =>
    frame.getAttribute('data-side') === side ? [faces[index]?.html ?? ''] : [],
  );
}

/**
 * Delivers one `answer {value}` message from the frame of the given side, the way the runtime posts it after the
 * learner edits an answer control. The frames must have received `show` first, so this waits for the recording.
 */
export async function postAnswerFromFrame(root: ParentNode, side: string, value: unknown): Promise<void> {
  await shownFaces(root);
  const frame = frames(root).find((candidate) => candidate.getAttribute('data-side') === side);
  if (!frame) throw new Error(`no card frame on side ${side}`);
  window.dispatchEvent(new MessageEvent('message', { data: { type: 'answer', value }, source: frame.contentWindow }));
  await drain();
}
