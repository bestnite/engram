import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRuntime, type FrameRuntime } from '../cardframe/runtime';
import { bodyClassName, browserClasses, platformClasses } from '../cardframe/reviewer';

/**
 * The reader's compatibility surface: the body and root classes Anki's reviewer adds, the template globals, and the
 * value every answer control posts. The parent window is faked exactly as in the runtime's own cases, because the frame
 * accepts messages only from the object it was built with.
 */
interface FakeParent {
  postMessage: ReturnType<typeof vi.fn>;
}

let parent: FakeParent;
let runtime: FrameRuntime | null = null;

function posted(): Array<Record<string, unknown>> {
  return parent.postMessage.mock.calls.map((call) => call[0] as Record<string, unknown>);
}

function answers(): unknown[] {
  return posted()
    .filter((message) => message.type === 'answer')
    .map((message) => message.value);
}

function fromParent(data: unknown): MessageEvent {
  return new MessageEvent('message', { data, source: parent as unknown as MessageEventSource });
}

function showMessage(overrides: Record<string, unknown> = {}): MessageEvent {
  return fromParent({
    type: 'show',
    html: '',
    css: '',
    bodyClass: 'card',
    side: 'question',
    mediaBase: '',
    autoplay: false,
    ordinal: 0,
    ...overrides,
  });
}

beforeEach(() => {
  parent = { postMessage: vi.fn() };
  document.head.innerHTML = '';
  document.body.innerHTML = '<div id="qa"></div>';
  document.documentElement.className = '';
  delete (window as unknown as { MathJax?: unknown }).MathJax;
  delete (window as unknown as { onUpdateHook?: unknown }).onUpdateHook;
  delete (window as unknown as { onShownHook?: unknown }).onShownHook;
});

afterEach(() => {
  runtime?.destroy();
  runtime = null;
  vi.restoreAllMocks();
});

const USER_AGENTS = {
  iphone:
    'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1',
  ipad: 'Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1',
  android:
    'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36',
  windowsFirefox: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0',
  macSafari:
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15',
  linuxChrome:
    'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36',
} as const;

describe('card frame browser and platform classes', () => {
  it('matches Anki browser_selector.ts for six user agents', () => {
    expect(browserClasses(USER_AGENTS.iphone)).toEqual(['iphone', 'ios', 'mobile', 'safari']);
    expect(browserClasses(USER_AGENTS.ipad)).toEqual(['ipad', 'ios', 'mobile', 'safari']);
    expect(browserClasses(USER_AGENTS.android)).toEqual(['android', 'mobile', 'chrome']);
    expect(browserClasses(USER_AGENTS.windowsFirefox)).toEqual(['win', 'firefox']);
    expect(browserClasses(USER_AGENTS.macSafari)).toEqual(['mac', 'safari']);
    expect(browserClasses(USER_AGENTS.linuxChrome)).toEqual(['linux', 'chrome']);
  });

  it('names the platform the way Anki does', () => {
    expect(platformClasses(USER_AGENTS.iphone)).toEqual(['isLin']);
    expect(platformClasses(USER_AGENTS.ipad)).toEqual(['isLin']);
    expect(platformClasses(USER_AGENTS.android)).toEqual(['isLin']);
    expect(platformClasses(USER_AGENTS.windowsFirefox)).toEqual(['isWin']);
    expect(platformClasses(USER_AGENTS.macSafari)).toEqual(['isMac']);
    expect(platformClasses(USER_AGENTS.linuxChrome)).toEqual(['isLin']);
  });

  it('writes the classes on the frame document at boot', () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    const classes = document.documentElement.classList;
    expect(classes.contains('mobile') || classes.contains('win') || classes.contains('linux') || classes.contains('mac')).toBe(true);
    expect(classes.contains('isMac') || classes.contains('isWin') || classes.contains('isLin')).toBe(true);
  });
});

describe('card frame reviewer body classes', () => {
  it('composes card, cardN and the two night spellings', () => {
    expect(bodyClassName('card', 0, false)).toBe('card card1');
    expect(bodyClassName('card', 2, true)).toBe('card card3 nightMode night_mode');
    // A server base class that is not `card` is kept, and `card` is still added.
    expect(bodyClassName('', 1, false)).toBe('card card2');
  });

  it('writes the ordinal on the body and follows the theme', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    const bootClasses = [...document.documentElement.classList];
    await runtime.handleMessage(showMessage({ ordinal: 2, side: 'question' }));
    expect(document.body.className).toBe('card card3');
    // A show changes only the theme class; the browser and platform classes written at boot survive it.
    for (const name of bootClasses) expect(document.documentElement.classList.contains(name)).toBe(true);

    await runtime.handleMessage(fromParent({ type: 'theme', dark: true }));
    expect(document.body.className).toBe('card card3 nightMode night_mode');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    for (const name of bootClasses) expect(document.documentElement.classList.contains(name)).toBe(true);
  });
});

describe('card frame answer controls post their value', () => {
  async function showControl(html: string): Promise<HTMLElement> {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(showMessage({ html, side: 'question' }));
    const root = document.getElementById('qa');
    if (!root) throw new Error('no content root');
    return root;
  }

  function fire(target: Element, type: 'input' | 'change'): void {
    target.dispatchEvent(new Event(type, { bubbles: true }));
  }

  it('posts the typed text', async () => {    const root = await showControl('<input id="text-answer" class="answer-input" data-control="text" type="text">');
    const input = root.querySelector<HTMLInputElement>('#text-answer')!;
    input.value = 'Paris';
    fire(input, 'input');
    expect(answers()).toEqual(['Paris']);
  });

  it('posts the numeric text as a string', async () => {
    const root = await showControl('<input id="numeric-answer" class="answer-input" data-control="numeric" type="text" inputmode="decimal">');
    const input = root.querySelector<HTMLInputElement>('#numeric-answer')!;
    input.value = '3.5';
    fire(input, 'input');
    expect(answers()).toEqual(['3.5']);
  });

  it('posts the stored option index of a single choice', async () => {
    const root = await showControl(
      '<div class="answer-choices" data-control="choice_single">' +
        '<label class="answer-choice"><input type="radio" name="g" value="0" data-control="choice_single"></label>' +
        '<label class="answer-choice"><input type="radio" name="g" value="1" data-control="choice_single"></label>' +
        '</div>',
    );
    const second = root.querySelectorAll<HTMLInputElement>('input[type=radio]')[1]!;
    second.checked = true;
    fire(second, 'change');
    expect(answers()).toEqual([1]);
  });

  it('posts the checked stored indices of a multiple choice in index order', async () => {
    const root = await showControl(
      '<div class="answer-choices" data-control="choice_multi">' +
        '<label class="answer-choice"><input type="checkbox" value="2" data-control="choice_multi"></label>' +
        '<label class="answer-choice"><input type="checkbox" value="0" data-control="choice_multi"></label>' +
        '</div>',
    );
    const boxes = root.querySelectorAll<HTMLInputElement>('input[type=checkbox]');
    boxes[0]!.checked = true;
    boxes[1]!.checked = true;
    fire(boxes[1]!, 'change');
    expect(answers()).toEqual([[0, 2]]);
  });

  it('posts the boolean of a true/false choice', async () => {
    const root = await showControl(
      '<div class="answer-choices" data-control="true_false">' +
        '<label class="answer-choice"><input type="radio" name="g" value="true" data-control="true_false"></label>' +
        '<label class="answer-choice"><input type="radio" name="g" value="false" data-control="true_false"></label>' +
        '</div>',
    );
    const second = root.querySelectorAll<HTMLInputElement>('input[type=radio]')[1]!;
    second.checked = true;
    fire(second, 'change');
    expect(answers()).toEqual([false]);
  });

  it('posts one string per list item in index order', async () => {
    const root = await showControl(
      '<div class="answer-inputs" data-control="list">' +
        '<input class="answer-input" data-control="list" data-index="0" type="text">' +
        '<input class="answer-input" data-control="list" data-index="1" type="text">' +
        '</div>',
    );
    const inputs = root.querySelectorAll<HTMLInputElement>('input');
    inputs[0]!.value = 'red';
    inputs[1]!.value = 'blue';
    fire(inputs[1]!, 'input');
    expect(answers()).toEqual([['red', 'blue']]);
  });

  it('posts one string per cloze blank in index order', async () => {
    const root = await showControl(
      '<div class="answer-inputs" data-control="cloze_blanks">' +
        '<input class="answer-input" data-control="cloze_blanks" data-index="0" type="text">' +
        '<input class="answer-input" data-control="cloze_blanks" data-index="1" type="text">' +
        '</div>',
    );
    const inputs = root.querySelectorAll<HTMLInputElement>('input');
    inputs[0]!.value = 'Paris';
    inputs[1]!.value = 'Rome';
    fire(inputs[0]!, 'input');
    expect(answers()).toEqual([['Paris', 'Rome']]);
  });

  it('posts the short-answer text', async () => {
    const root = await showControl('<textarea id="short_answer-answer" class="answer-input" data-control="short_answer"></textarea>');
    const area = root.querySelector<HTMLTextAreaElement>('textarea')!;
    area.value = 'a written answer';
    fire(area, 'input');
    expect(answers()).toEqual(['a written answer']);
  });

  it('posts the typed answer on Enter in a type-in input', async () => {
    const root = await showControl('<p>Question</p><input id="typeans" type="text" autocomplete="off">');
    const input = root.querySelector<HTMLInputElement>('#typeans')!;
    input.value = 'Paris';
    input.focus();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true }));

    // The typed value reaches the parent so the answer face can compare it; Enter also travels as the reveal key.
    expect(answers()).toEqual(['Paris']);
    expect(posted().some((message) => message.type === 'key' && message.key === 'Enter')).toBe(true);

    // A non-Enter key stays with the input and posts no answer.
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a', code: 'KeyA' }));
    expect(answers()).toEqual(['Paris']);

    // Typing also posts the value, so revealing with the button compares the latest text without pressing Enter.
    input.value = 'Lyon';
    fire(input, 'input');
    expect(answers()).toEqual(['Paris', 'Lyon']);
  });
});

describe('card frame reviewer globals', () => {
  it('exposes jQuery under both names and the typed answer', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(showMessage({ html: '<input class="answer-input" data-control="text" type="text">' }));
    const input = document.querySelector<HTMLInputElement>('input.answer-input')!;
    input.value = 'typed';
    const win = window as unknown as { $?: unknown; jQuery?: unknown; getTypedAnswer?: () => string };
    expect(typeof win.$).toBe('function');
    expect(win.$).toBe(win.jQuery);
    expect(win.getTypedAnswer?.()).toBe('typed');
  });

  it('accepts ans and play commands and ignores everything else', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(
      showMessage({ html: '<p>[sound:one.mp3]</p><input class="answer-input" data-control="text" type="text">' }),
    );
    const input = document.querySelector<HTMLInputElement>('input.answer-input')!;
    input.value = 'typed';
    fire(input, 'input');

    const win = window as unknown as { pycmd?: (command: unknown) => void; bridgeCommand?: (command: unknown) => void };
    expect(win.pycmd).toBe(win.bridgeCommand);

    const play = vi.fn();
    document.querySelector('button[data-shortcode-control="audio"]')?.addEventListener('click', play);
    win.pycmd?.('play:q:0');
    expect(play).toHaveBeenCalledTimes(1);
    // The answer side has nothing to play in this face.
    win.pycmd?.('play:a:0');
    expect(play).toHaveBeenCalledTimes(1);
    win.pycmd?.('play:q:1');
    expect(play).toHaveBeenCalledTimes(1);

    const before = answers().length;
    win.pycmd?.('ans');
    expect(answers().slice(before)).toEqual(['typed']);
    win.pycmd?.('deckBrowser');
    win.pycmd?.(42);
    expect(answers().slice(before)).toEqual(['typed']);
  });
});

function fire(target: Element, type: 'input' | 'change'): void {
  target.dispatchEvent(new Event(type, { bubbles: true }));
}
