import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import { get } from 'svelte/store';
import NoteEditView from '../lib/views/NoteEditView.svelte';
import { routeStore } from '../lib/router';
import { routes } from '../lib/router/routes';
import { apiClient, ApiClientError, type Note, type NotePreviewResponse } from '../lib/api';
import { seedCardTypes, clearCardTypes } from './card-type-fixture';
import { typeset } from '../lib/mathjax';
import { setLocale, formatMessage } from '../lib/i18n';

// MathJax 由模块边界替身接管：用例断言「排版确实被调用、且只对预览容器调用」，
// 不依赖真实 MathJax 资源（测试环境没有自托管脚本）。
vi.mock('../lib/mathjax', () => ({ typeset: vi.fn(async () => {}) }));

const typesetSpy = vi.mocked(typeset);
const SHA = 'a'.repeat(64);
const PREVIEW_URL = `/media/${SHA}`;

/** 手动控制落点时刻的 promise，用来制造「后发先至」的过期响应/错误。 */
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function noteFixture(overrides: Partial<Note> = {}): Note {
  return {
    id: '9',
    deck_id: '5',
    kind: 'basic',
    fields: { front: 'Q', back: 'A' },
    tags: [],
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    external_ref: '',
    suspended: false,
    ...overrides,
  };
}

function createResponse(cards: Array<{ front_html: string; back_html: string }>): NotePreviewResponse {
  return { cards };
}

let target: HTMLElement;
const mountedInstances: Array<ReturnType<typeof mount>> = [];

function mountEditor(params: Record<string, string>): void {
  seedCardTypes();
  routeStore.set({ path: '/decks/5/notes', params, query: {}, search: '', route: null });
  target = document.createElement('div');
  document.body.appendChild(target);
  mountedInstances.push(mount(NoteEditView, { target }));
}

/** 冲掉微任务并同步应用 Svelte 的待处理状态，让断言看到真实 DOM。 */
async function flush(times = 24): Promise<void> {
  for (let i = 0; i < times; i++) await Promise.resolve();
  flushSync();
}

function setField(testid: string, value: string): void {
  const el = target.querySelector(`[data-testid="${testid}"]`) as HTMLInputElement | HTMLTextAreaElement;
  el.value = value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
}

async function selectKind(label: string): Promise<void> {
  const trigger = target.querySelector('[data-testid="note-edit-kind"]') as HTMLElement;
  trigger.focus();
  trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  await flush();
  const option = Array.from(document.body.querySelectorAll('[role="option"]')).find(
    (el) => el.textContent?.trim() === label
  ) as HTMLElement;
  // bits-ui 的选项在 pointerdown/pointerup 上提交选择（click 单独派发不会选中）。
  option.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true }));
  option.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, cancelable: true }));
  await flush();
}

function submitForm(): void {
  const form = target.querySelector('form') as HTMLFormElement;
  form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
}

function previewFront(index = 0): string {
  return target.querySelector(`[data-testid="note-preview-front-${index}"]`)?.textContent ?? '';
}

beforeEach(() => {
  setLocale('zh-CN');
  vi.useFakeTimers();
  vi.clearAllMocks();
});

afterEach(() => {
  while (mountedInstances.length > 0) unmount(mountedInstances.pop()!);
  vi.restoreAllMocks();
  vi.useRealTimers();
  document.body.innerHTML = '';
  clearCardTypes();
});

describe('unified note editor — create mode', () => {
  it('renders the full editor with an automatic (button-less) preview and no empty-id note read', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const getNote = vi.spyOn(apiClient, 'getNote');

    mountEditor({ id: '5' });
    await flush();

    expect(target.querySelector('[data-testid="note-editor-form"]')).toBeTruthy();
    // 预览按钮已彻底移除：既没有旧 testid，也没有残留引用（键已从语言包删除）。
    expect(target.querySelector('[data-testid="note-preview"]')).toBeNull();
    expect(target.innerHTML).not.toContain('note_preview.action');
    // 未输入内容：走 empty 态，且不向服务端发预览请求。
    expect(target.querySelector('[data-testid="note-preview-empty"]')).toBeTruthy();
    expect(previewNote).not.toHaveBeenCalled();
    // 初始化绝不带空 id GET 远端卡片。
    expect(getNote).not.toHaveBeenCalled();
  });

  it('debounces reactive preview and renders the latest result', async () => {
    const previewNote = vi
      .spyOn(apiClient, 'previewNote')
      .mockResolvedValue(createResponse([{ front_html: '<p>rendered Q</p>', back_html: '<p>A</p>' }]));

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'Q');
    await flush();

    await vi.advanceTimersByTimeAsync(499);
    expect(previewNote).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    await flush();

    expect(previewNote).toHaveBeenCalledTimes(1);
    expect(previewNote).toHaveBeenCalledWith('5', 'basic', { front: 'Q' });
    expect(previewFront()).toContain('rendered Q');
    expect(target.querySelector('[data-testid="note-preview-result"]')).toBeTruthy();
  });

  it('resets the debounce window on every keystroke (only the last one fires)', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(300);
    setField('note-field-front', 'QR');
    await flush();

    // 距第一次输入已 500ms，但第二次输入重置了窗口，此刻仍不该发请求。
    await vi.advanceTimersByTimeAsync(200);
    expect(previewNote).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(300);
    await flush();

    expect(previewNote).toHaveBeenCalledTimes(1);
    expect(previewNote).toHaveBeenCalledWith('5', 'basic', { front: 'QR' });
  });

  it('ignores a stale delayed response and keeps the newest preview', async () => {
    const first = deferred<NotePreviewResponse>();
    const second = deferred<NotePreviewResponse>();
    const previewNote = vi
      .spyOn(apiClient, 'previewNote')
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'A');
    await flush();
    await vi.advanceTimersByTimeAsync(500); // 请求 1 在途
    setField('note-field-front', 'B');
    await flush();
    await vi.advanceTimersByTimeAsync(500); // 请求 2 在途
    expect(previewNote).toHaveBeenCalledTimes(2);

    second.resolve(createResponse([{ front_html: '<p>SECOND</p>', back_html: '' }]));
    await flush();
    expect(previewFront()).toContain('SECOND');

    // 旧请求此刻才回来：必须被丢弃，不能覆盖更新的结果。
    first.resolve(createResponse([{ front_html: '<p>FIRST</p>', back_html: '' }]));
    await flush();
    expect(previewFront()).toContain('SECOND');
    expect(previewFront()).not.toContain('FIRST');
  });

  it('ignores a stale delayed error after a newer success', async () => {
    const first = deferred<NotePreviewResponse>();
    const previewNote = vi
      .spyOn(apiClient, 'previewNote')
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce(createResponse([{ front_html: '<p>GOOD</p>', back_html: '' }]));

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'A');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    setField('note-field-front', 'B');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    expect(previewNote).toHaveBeenCalledTimes(2);
    await flush();
    expect(previewFront()).toContain('GOOD');

    // 过期请求随后失败：不得把已就绪的预览翻成错误态。
    first.reject(new ApiClientError('HTTP 500', { status: 500, code: 'internal_error' }));
    await flush();
    expect(previewFront()).toContain('GOOD');
    expect(target.querySelector('[data-testid="note-preview-error"]')).toBeNull();
  });

  it('shows the incomplete state on a 400 preview response', async () => {
    vi.spyOn(apiClient, 'previewNote').mockRejectedValue(
      new ApiClientError('HTTP 400', { status: 400, code: 'invalid_request' })
    );

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(target.querySelector('[data-testid="note-preview-incomplete"]')).toBeTruthy();
    expect(target.querySelector('[data-testid="note-preview-error"]')).toBeNull();
  });

  it('treats a blank bool-bearing type as empty and never previews it', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote');

    mountEditor({ id: '5' });
    await flush();

    // typed 的空表单默认带 ignore_case / ignore_whitespace = true：这些默认值不是用户输入，
    // 不能算作「有内容」，否则一张空表单会去请求预览。
    await selectKind(formatMessage('zh-CN', 'notes.kind.typed'));
    expect(target.querySelector('[data-testid="note-field-prompt"]')).toBeTruthy();
    await flush();

    await vi.advanceTimersByTimeAsync(600);
    await flush();

    expect(previewNote).not.toHaveBeenCalled();
    expect(target.querySelector('[data-testid="note-preview-empty"]')).toBeTruthy();
  });

  it('counts a numeric zero as real input and previews it', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    await selectKind(formatMessage('zh-CN', 'notes.kind.numeric'));
    // 只填数值字段（值 0）：空串基线与 0 不同，0 必须算作内容并发起预览。
    setField('note-field-value', '0');
    await flush();

    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(previewNote).toHaveBeenCalledWith('5', 'numeric', { value: 0 });
  });

  it('typesets only the sanitized preview container through MathJax', async () => {
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(
      createResponse([{ front_html: '<p>$x$</p>', back_html: '' }])
    );

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(typesetSpy).toHaveBeenCalled();
    const container = target.querySelector('[data-testid="note-preview-result"]');
    expect(container).toBeTruthy();
    const [nodes] = typesetSpy.mock.calls.at(-1)!;
    expect(nodes).toEqual([container]);
  });

  it('serializes commit and typeset so a delayed typeset only ever sees its own generation', async () => {
    // 第一轮排版挂起：若「写入新卡片」与排版不串行，第二轮响应会在第一轮排版中途替换容器，
    // 第一轮的 MathJax 就会改写第二轮刚提交的 DOM。这里记录每次排版调用时容器的实际文本。
    const firstTypeset = deferred<void>();
    const seen: string[] = [];
    typesetSpy.mockImplementation(async (nodes) => {
      seen.push((nodes[0] as HTMLElement | null)?.textContent ?? '');
      if (seen.length === 1) await firstTypeset.promise;
    });
    vi.spyOn(apiClient, 'previewNote')
      .mockResolvedValueOnce(createResponse([{ front_html: '<p>FIRST</p>', back_html: '' }]))
      .mockResolvedValueOnce(createResponse([{ front_html: '<p>SECOND</p>', back_html: '' }]));

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'A');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();
    expect(previewFront()).toContain('FIRST');
    expect(seen).toHaveLength(1);
    expect(seen[0]).toContain('FIRST');

    setField('note-field-front', 'B');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    await flush();

    // 第二轮响应已到，但排版链仍被第一轮占住：当前显示的必须还是第一轮内容，
    // 第二轮尚未开始排版（否则第一轮的延迟排版会落到第二轮的容器上）。
    expect(previewFront()).toContain('FIRST');
    expect(seen).toHaveLength(1);

    firstTypeset.resolve();
    await flush();

    // 第一轮排版结束后第二轮才提交并排版：显示最新内容，且排版看到的是它自己那一代。
    expect(previewFront()).toContain('SECOND');
    expect(seen).toHaveLength(2);
    expect(seen[1]).toContain('SECOND');
  });

  it('creates a non-basic card and redirects to the deck page only on success', async () => {
    const createNotes = vi.spyOn(apiClient, 'createNotes').mockResolvedValue({
      created: 1,
      updated: 0,
      skipped: 0,
      errors: [],
      dry_run: false,
    });
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    await selectKind(formatMessage('zh-CN', 'notes.kind.cloze'));
    expect(target.querySelector('[data-testid="note-field-text"]')).toBeTruthy();
    setField('note-field-text', 'The {{c1::answer}}');
    setField('note-tags-editor', 'topic, extra');
    await flush();

    submitForm();
    await flush();

    expect(createNotes).toHaveBeenCalledWith('5', {
      notes: [{ kind: 'cloze', fields: { text: 'The {{c1::answer}}' }, tags: ['topic', 'extra'] }],
    });
    expect(get(routeStore).path).toBe('/decks/5');
  });

  it('stays on the page with the content intact when creation reports a row error', async () => {
    const createNotes = vi.spyOn(apiClient, 'createNotes').mockResolvedValue({
      created: 0,
      updated: 0,
      skipped: 0,
      errors: [{ index: 0, reason: 'invalid' }],
      dry_run: false,
    });
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'kept');
    await flush();

    submitForm();
    await flush();

    expect(createNotes).toHaveBeenCalledTimes(1);
    // 没有跳转，内容仍在，错误可见。
    expect(get(routeStore).path).not.toBe('/decks/5');
    expect(target.querySelector('[data-testid="note-save-error"]')).toBeTruthy();
    expect((target.querySelector('[data-testid="note-field-front"]') as HTMLTextAreaElement).value).toBe('kept');
  });

  it('does not navigate on a dry_run success', async () => {
    vi.spyOn(apiClient, 'createNotes').mockResolvedValue({
      created: 1,
      updated: 0,
      skipped: 0,
      errors: [],
      dry_run: true,
    });
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();

    submitForm();
    await flush();

    expect(get(routeStore).path).not.toBe('/decks/5');
    expect(target.querySelector('[data-testid="note-save-error"]')).toBeTruthy();
  });

  it('prevents duplicate in-flight create submissions', async () => {
    const pending = deferred<Awaited<ReturnType<typeof apiClient.createNotes>>>();
    const createNotes = vi.spyOn(apiClient, 'createNotes').mockReturnValue(pending.promise);
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();

    submitForm();
    await flush();
    submitForm();
    await flush();

    expect(createNotes).toHaveBeenCalledTimes(1);
    pending.resolve({ created: 1, updated: 0, skipped: 0, errors: [], dry_run: false });
    await flush();
    expect(get(routeStore).path).toBe('/decks/5');
  });

  it('does not navigate when the view unmounts while a create is in flight', async () => {
    const pending = deferred<Awaited<ReturnType<typeof apiClient.createNotes>>>();
    vi.spyOn(apiClient, 'createNotes').mockReturnValue(pending.promise);
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'Q');
    await flush();
    submitForm();
    await flush();
    expect(pending.promise).toBeTruthy();

    // 用户在新建请求在途时离开：成功后不得把他拉去卡组页。
    unmount(mountedInstances.pop()!);
    const pathBefore = get(routeStore).path;

    pending.resolve({ created: 1, updated: 0, skipped: 0, errors: [], dry_run: false });
    await flush();

    expect(get(routeStore).path).toBe(pathBefore);
  });

  it('inserts a picked media item into the focused field and re-runs the preview', async () => {
    vi.spyOn(apiClient, 'listMedia').mockResolvedValue({
      items: [{ sha256: SHA, mime: 'image/png', bytes: 4, url: PREVIEW_URL, created_at: '' }],
      next_cursor: '',
    });
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    (target.querySelector('[data-testid="media-picker-toggle"]') as HTMLButtonElement).click();
    await flush();
    (target.querySelector(`[data-testid="media-item-${SHA}"]`) as HTMLButtonElement).click();
    await flush();

    expect((target.querySelector('[data-testid="note-field-front"]') as HTMLTextAreaElement).value).toBe(
      `![](${PREVIEW_URL})`
    );

    await vi.advanceTimersByTimeAsync(500);
    await flush();
    expect(previewNote).toHaveBeenCalledWith('5', 'basic', { front: `![](${PREVIEW_URL})` });
  });
});

describe('unified note editor — edit mode', () => {
  it('loads an existing note and runs the initial preview automatically', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture());
    const previewNote = vi
      .spyOn(apiClient, 'previewNote')
      .mockResolvedValue(createResponse([{ front_html: '<p>Q</p>', back_html: '<p>A</p>' }]));

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    expect((target.querySelector('[data-testid="note-field-front"]') as HTMLTextAreaElement).value).toBe('Q');

    await vi.advanceTimersByTimeAsync(500);
    await flush();

    expect(previewNote).toHaveBeenCalledWith('5', 'basic', { front: 'Q', back: 'A' });
    expect(previewFront()).toContain('Q');
  });

  it('saves in place without redirecting and round-trips unknown fields', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(
      noteFixture({ fields: { front: 'Q', back: 'A', custom: 'keep' } })
    );
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const updateNote = vi.spyOn(apiClient, 'updateNote').mockResolvedValue(
      noteFixture({ fields: { front: 'Q2', back: 'A', custom: 'keep' }, tags: ['a', 'b'] })
    );

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    setField('note-field-front', 'Q2');
    setField('note-tags-editor', 'a, b');
    await flush();

    submitForm();
    await flush();

    // 未知键原样带回；身份是原卡片 id；不跳转。
    expect(updateNote).toHaveBeenCalledWith('9', {
      kind: 'basic',
      fields: { front: 'Q2', back: 'A', custom: 'keep' },
      tags: ['a', 'b'],
    });
    expect(get(routeStore).path).not.toBe('/decks/5');
    expect(target.querySelector('[data-testid="note-saved"]')).toBeTruthy();
  });

  it('keeps typing that arrives during a save in flight and never claims it was saved', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture({ fields: { front: 'Q', back: 'A' } }));
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const pending = deferred<Note>();
    const updateNote = vi.spyOn(apiClient, 'updateNote').mockReturnValue(pending.promise);

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    setField('note-field-front', 'QQ');
    await flush();
    submitForm();
    await flush();
    expect(updateNote).toHaveBeenCalledTimes(1);

    // 请求在途时继续输入。
    setField('note-field-front', 'QQ-newer');
    await flush();

    // 服务端回的是提交那一刻的快照：绝不能用它覆盖用户正在输入的内容。
    pending.resolve(noteFixture({ fields: { front: 'QQ', back: 'A' } }));
    await flush();

    expect((target.querySelector('[data-testid="note-field-front"]') as HTMLTextAreaElement).value).toBe('QQ-newer');
    // 当前草稿并未被这次保存覆盖，也就不能显示「已保存」。
    expect(target.querySelector('[data-testid="note-saved"]')).toBeNull();
  });

  it('clears the saved marker once the draft changes again, including tags', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture());
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    vi.spyOn(apiClient, 'updateNote').mockResolvedValue(
      noteFixture({ fields: { front: 'Q2', back: 'A' }, tags: ['a'] })
    );

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    setField('note-field-front', 'Q2');
    setField('note-tags-editor', 'a');
    await flush();
    submitForm();
    await flush();
    expect(target.querySelector('[data-testid="note-saved"]')).toBeTruthy();

    // 保存之后又改标签：草稿不再等于已保存快照，标记必须撤下。
    setField('note-tags-editor', 'a, b');
    await flush();
    expect(target.querySelector('[data-testid="note-saved"]')).toBeNull();
  });

  it('shows a not-found state when the note belongs to another deck', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture({ deck_id: '999' }));

    mountEditor({ id: '5', noteId: '9' });
    await flush();

    expect(target.querySelector('[data-testid="note-editor-form"]')).toBeNull();
    expect(target.querySelector('[data-testid="note-edit-not-found"]')).toBeTruthy();
    expect(target.textContent).toContain('卡片不存在');
  });
});

describe('unified note editor — lifecycle and routes', () => {
  it('cancels the pending debounce on unmount', async () => {
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));

    mountEditor({ id: '5' });
    await flush();

    setField('note-field-front', 'Q');
    await flush();
    unmount(mountedInstances.pop()!);

    await vi.advanceTimersByTimeAsync(1000);
    expect(previewNote).not.toHaveBeenCalled();
  });

  it('ignores an in-flight preview result after unmount', async () => {
    const pending = deferred<NotePreviewResponse>();
    const previewNote = vi.spyOn(apiClient, 'previewNote').mockReturnValue(pending.promise);

    mountEditor({ id: '5' });
    await flush();
    setField('note-field-front', 'R');
    await flush();
    await vi.advanceTimersByTimeAsync(500);
    expect(previewNote).toHaveBeenCalledTimes(1);

    unmount(mountedInstances.pop()!);
    pending.resolve(createResponse([{ front_html: '<p>late</p>', back_html: '' }]));
    await flush();

    // 卸载后落地的结果不得触发 MathJax 排版（也不会写状态）。
    expect(typesetSpy).not.toHaveBeenCalled();
  });

  it('ignores an in-flight save result after unmount (no state write, no typeset)', async () => {
    vi.spyOn(apiClient, 'getNote').mockResolvedValue(noteFixture());
    vi.spyOn(apiClient, 'previewNote').mockResolvedValue(createResponse([]));
    const pending = deferred<Note>();
    vi.spyOn(apiClient, 'updateNote').mockReturnValue(pending.promise);

    mountEditor({ id: '5', noteId: '9' });
    await flush();
    setField('note-field-front', 'Q2');
    await flush();
    submitForm();
    await flush();

    const typesetCallsBefore = typesetSpy.mock.calls.length;
    unmount(mountedInstances.pop()!);

    // 保存请求在卸载后才落地：不得再写状态，也不得触发任何排版。
    pending.resolve(noteFixture({ fields: { front: 'Q2', back: 'A' } }));
    await flush();

    expect(typesetSpy.mock.calls.length).toBe(typesetCallsBefore);
  });

  it('points every create/edit route alias at the one shared editor implementation', () => {
    const byName = (name: string) => routes.find((route) => route.name === name);
    const editor = byName('note-edit')!.component;
    expect(byName('note-edit-nid')!.component).toBe(editor);
    expect(byName('note-create')!.component).toBe(editor);
    expect(byName('note-create-legacy')!.component).toBe(editor);
  });
});
