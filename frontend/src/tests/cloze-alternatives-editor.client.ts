import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ClozeAlternativesEditor from '../lib/components/ClozeAlternativesEditor.svelte';
import ClozeAlternativesHarness from './cloze-alternatives-harness.svelte';
import { setLocale } from '../lib/i18n';
import { extractPhysicalBlanks, htmlToPlain, parseClozeOpen } from '../lib/cloze-alternatives';
import { en } from '../lib/i18n/locales/en';
import { zhCN } from '../lib/i18n/locales/zh-CN';

describe('ClozeAlternativesEditor client interactions', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  function renderEditor(props: { textFieldValue: string; metaValue: string; onchange?: (meta: string) => void }) {
    setLocale('zh-CN');
    target = document.createElement('div');
    document.body.appendChild(target);
    const onchange = props.onchange ?? vi.fn();
    instance = mount(ClozeAlternativesEditor, {
      target,
      props: {
        textFieldValue: props.textFieldValue,
        metaValue: props.metaValue,
        onchange,
      },
    });
    return { target, onchange };
  }

  it('matches Go extraction fixtures and safely decodes escaped markup as text', () => {
    // These expected results are shared with internal/notetype/answerspec_test.go and cloze_test.go.
    const fixture = 'one {{c1::two}} three {{c1::four::a hint}} five {{c2::six}}';
    const blanks = extractPhysicalBlanks(fixture);
    expect(blanks.filter(b => b.clozeOrdinal === 1).map(b => b.canonical)).toEqual(['two', 'four']);
    expect(blanks.filter(b => b.clozeOrdinal === 2).map(b => b.canonical)).toEqual(['six']);
    const multi = extractPhysicalBlanks('{{c1,2::shared}} and {{c1::first}} vs {{c2::second}}');
    expect(multi.filter(b => b.clozeOrdinal === 1).map(b => b.canonical)).toEqual(['shared', 'first']);
    expect(multi.filter(b => b.clozeOrdinal === 2).map(b => b.canonical)).toEqual(['shared', 'second']);
    expect(parseClozeOpen('{{c,0001,,65536,0::x}}')?.ordinals).toEqual([0, 1]);
    expect(extractPhysicalBlanks('{{c0::outer {{c1::inner}}}}').map(b => b.canonical)).toEqual(['inner']);
    expect(htmlToPlain('&lt;/textarea&gt;&lt;img src=x onerror=alert(1)&gt;')).toBe('</textarea><img src=x onerror=alert(1)>');
    expect(document.querySelector('img')).toBeNull();
  });

  it('keeps English and Chinese catalog key sets identical', () => {
    expect(Object.keys(en).sort()).toEqual(Object.keys(zhCN).sort());
  });

  it('keeps associations by number and canonical across insertions, reorder and rename', async () => {
    setLocale('zh-CN');
    target = document.createElement('div');
    document.body.appendChild(target);
    const onchange = vi.fn();
    instance = mount(ClozeAlternativesHarness, { target, props: {
      textFieldValue: '{{c1::is}} {{c1::are}} {{c7::is}}',
      metaValue: JSON.stringify({ cloze_accept: { '1': { is: ['was'], are: ['were'] }, '7': { is: ['be'] } } }),
      onchange,
    } });
    await flush();
    const setText = async (value: string) => {
      const input = target.querySelector('[data-testid="harness-text"]') as HTMLInputElement;
      input.value = value;
      input.dispatchEvent(new Event('input', { bubbles: true }));
      await flush();
    };
    const pillsFor = (ordinal: number, index: number) => target.querySelector(`[data-testid="cloze-alt-pills-${ordinal}-${index}"]`)?.textContent ?? '';
    expect(pillsFor(1, 0)).toContain('was');
    expect(pillsFor(1, 1)).toContain('were');
    expect(pillsFor(7, 2)).toContain('be');
    await setText('{{c1::new}} {{c7::is}} {{c1::are}} {{c1::is}}');
    expect(pillsFor(1, 0)).toBe('');
    expect(pillsFor(7, 1)).toContain('be');
    expect(pillsFor(1, 2)).toContain('were');
    expect(pillsFor(1, 3)).toContain('was');
    await setText('{{c1::new}} {{c7::is}} {{c1::are}} {{c1::renamed}}');
    expect(pillsFor(1, 3)).toBe('');
    expect(target.querySelector('[data-testid="cloze-alt-unlinked-section"]')?.textContent).toContain('was');
    expect(onchange).not.toHaveBeenCalled();
  });

  it('exposes a damaged unlinked group and removes it only on explicit action', async () => {
    const onchange = vi.fn();
    const { target } = renderEditor({ textFieldValue: '{{c1::Paris}}', metaValue: '{"cloze_accept":{"99":null}}', onchange });
    await flush();
    expect(target.querySelector('[data-testid="cloze-alt-unlinked-invalid-group"]')?.textContent).toContain('c99');
    expect(onchange).not.toHaveBeenCalled();
    (target.querySelector('[data-testid="cloze-alt-remove-invalid-group-99"]') as HTMLButtonElement).click();
    await flush();
    expect(JSON.parse(onchange.mock.calls.at(-1)![0]).cloze_accept).toEqual({});
  });

  it('removes a normal pill explicitly and emits the updated list', async () => {
    const onchange = vi.fn();
    const { target } = renderEditor({ textFieldValue: '{{c1::Paris}}', metaValue: '{"cloze_accept":{"1":{"Paris":["Lutetia","City"]}}}', onchange });
    await flush();
    (target.querySelector('[data-testid="cloze-alt-remove-pill-0"]') as HTMLButtonElement).click();
    await flush();
    expect(JSON.parse(onchange.mock.calls.at(-1)![0]).cloze_accept['1'].Paris).toEqual(['City']);
    expect(target.querySelector('[data-testid="cloze-alt-pill"]')?.textContent).toContain('City');
  });

  it('renders shared notice for 2 identical blanks and >=3 blanks', async () => {
    // 2 identical blanks
    const { target: target2 } = renderEditor({
      textFieldValue: '{{c1::is}} Paris, and {{c1::is}} Rome.',
      metaValue: '',
    });
    await flush();

    const notice2 = target2.querySelector('[data-testid="cloze-alt-shared-notice"]');
    expect(notice2).not.toBeNull();
    expect(notice2!.textContent).toBe('这两处正文答案相同，共用同一组备选答案。');

    unmount(instance!);
    instance = null;

    // 3 identical blanks
    const { target: target3 } = renderEditor({
      textFieldValue: '{{c1::is}} A, {{c1::is}} B, and {{c1::is}} C.',
      metaValue: '',
    });
    await flush();

    const notice3 = target3.querySelector('[data-testid="cloze-alt-shared-notice"]');
    expect(notice3).not.toBeNull();
    expect(notice3!.textContent).toBe('这 3 处正文答案相同，共用同一组备选答案。');
  });

  it('does not split literal punctuation (commas, slashes, pipes) in candidate wordings', async () => {
    const onchange = vi.fn();
    const { target } = renderEditor({
      textFieldValue: '{{c1::Paris}} is the capital.',
      metaValue: '',
      onchange,
    });
    await flush();

    const input = target.querySelector('input[data-testid^="cloze-alt-input-"]') as HTMLInputElement;
    const addBtn = target.querySelector('button[data-testid^="cloze-alt-add-btn-"]') as HTMLButtonElement;

    expect(input).not.toBeNull();
    expect(addBtn).not.toBeNull();

    // Type a wording containing commas, pipes, and slashes
    input.value = 'City of Light, or / | Lutetia';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();

    addBtn.click();
    await flush();

    expect(onchange).toHaveBeenCalled();
    const serialized = JSON.parse(onchange.mock.calls.at(-1)![0]);
    expect(serialized.cloze_accept['1']['Paris']).toEqual(['City of Light, or / | Lutetia']);

    // Check pill rendered as one single item
    const pills = target.querySelectorAll('[data-testid="cloze-alt-pill"]');
    expect(pills).toHaveLength(1);
    expect(pills[0]!.textContent).toContain('City of Light, or / | Lutetia');
  });

  it('ignores Enter key while IME is composing, but submits on normal Enter', async () => {
    const onchange = vi.fn();
    const { target } = renderEditor({
      textFieldValue: '{{c1::Paris}} is the capital.',
      metaValue: '',
      onchange,
    });
    await flush();

    const input = target.querySelector('input[data-testid^="cloze-alt-input-"]') as HTMLInputElement;
    const form = document.createElement('form');
    input.parentElement!.before(form);
    form.appendChild(input);
    const submit = vi.fn((event: Event) => event.preventDefault());
    form.addEventListener('submit', submit);
    input.value = '巴黎';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();

    // 1. Enter key pressed while IME is composing: must NOT add
    input.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true }));
    const composingEvent = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
    Object.defineProperty(composingEvent, 'isComposing', { value: true });
    input.dispatchEvent(composingEvent);
    await flush();

    expect(onchange).not.toHaveBeenCalled();
    expect(composingEvent.defaultPrevented).toBe(true);
    const trackedEvent = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
    input.dispatchEvent(trackedEvent);
    expect(trackedEvent.defaultPrevented).toBe(true);
    input.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true }));
    const legacyEvent = new KeyboardEvent('keydown', { key: 'Enter', keyCode: 229, bubbles: true, cancelable: true });
    input.dispatchEvent(legacyEvent);
    expect(legacyEvent.defaultPrevented).toBe(true);
    expect(onchange).not.toHaveBeenCalled();
    expect(submit).not.toHaveBeenCalled();

    // 2. Normal Enter key pressed after composition finishes: adds candidate
    const normalEvent = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true });
    Object.defineProperty(normalEvent, 'isComposing', { value: false });
    input.dispatchEvent(normalEvent);
    await flush();

    expect(onchange).toHaveBeenCalled();
    const serialized = JSON.parse(onchange.mock.calls.at(-1)![0]);
    expect(serialized.cloze_accept['1']['Paris']).toEqual(['巴黎']);
  });

  it('guards invalid groups and unlinked invalid entries until explicit repair or removal', async () => {
    const onchange = vi.fn();
    const { target } = renderEditor({
      textFieldValue: '{{c1::Paris}} {{c2::Rome}}',
      metaValue: JSON.stringify({ future_key: { nested: [null, { a: true }] }, cloze_accept: { '1': { Old: 42 }, '2': null } }),
      onchange,
    });
    await flush();
    expect(target.querySelector('[data-testid="cloze-alt-invalid-group-warning"]')).not.toBeNull();
    expect(target.querySelector('[data-testid="cloze-alt-unlinked-item"]')?.textContent).toContain('Old');
    expect((target.querySelector('[data-testid="cloze-alt-input-2-1"]') as HTMLInputElement).disabled).toBe(true);
    const input = target.querySelector('[data-testid="cloze-alt-input-1-0"]') as HTMLInputElement;
    input.value = 'Lutetia';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    (target.querySelector('[data-testid="cloze-alt-add-btn-1-0"]') as HTMLButtonElement).click();
    await flush();
    const result = JSON.parse(onchange.mock.calls.at(-1)![0]);
    expect(result.cloze_accept['1'].Old).toBe(42);
    expect(result.cloze_accept['2']).toBeNull();
    expect(result.future_key).toEqual({ nested: [null, { a: true }] });
    (target.querySelector('[data-testid="cloze-alt-repair-group-btn"]') as HTMLButtonElement).click();
    await flush();
    expect(JSON.parse(onchange.mock.calls.at(-1)![0]).cloze_accept['2']).toEqual({});
    (target.querySelector('[data-testid="cloze-alt-unlinked-delete-0"]') as HTMLButtonElement).click();
    await flush();
    expect(JSON.parse(onchange.mock.calls.at(-1)![0]).cloze_accept['1'].Old).toBeUndefined();
  });

  it('explains that an invalid unlinked source cannot be reassociated without discarding it', async () => {
    const onchange = vi.fn();
    const { target } = renderEditor({ textFieldValue: '{{c1::Paris}}', metaValue: '{"cloze_accept":{"1":{"Old":["x",7]}}}', onchange });
    await flush();
    (target.querySelector('[data-testid="cloze-alt-reassociate-select-0"]') as HTMLElement).click();
    await flush();
    const option = Array.from(document.body.querySelectorAll('[role="option"]')).find(el => el.textContent?.trim() === 'c1: Paris') as HTMLElement;
    expect(option).toBeTruthy();
    option.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true }));
    option.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, cancelable: true }));
    await flush();
    expect(target.querySelector('[role="alert"]')?.textContent).toContain('此未关联条目包含损坏数据');
    expect(onchange).not.toHaveBeenCalled();
  });

  it('validates against canonical answer and existing duplicates', async () => {
    const onchange = vi.fn();
    const { target } = renderEditor({
      textFieldValue: '{{c1::Paris}} is the capital.',
      metaValue: JSON.stringify({ cloze_accept: { '1': { Paris: ['City of Light'] } } }),
      onchange,
    });
    await flush();

    const input = target.querySelector('input[data-testid^="cloze-alt-input-"]') as HTMLInputElement;
    const addBtn = target.querySelector('button[data-testid^="cloze-alt-add-btn-"]') as HTMLButtonElement;

    // Try to add the canonical answer "paris" (case-insensitive)
    input.value = '  paris ';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    addBtn.click();
    await flush();

    expect(onchange).not.toHaveBeenCalled();
    const errCanonical = target.querySelector('[data-testid="cloze-alt-error"]');
    expect(errCanonical).not.toBeNull();
    expect(errCanonical!.textContent).toContain('备选答案不能与正文答案相同');

    // Try to add existing duplicate "city of light"
    input.value = 'city of light';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    addBtn.click();
    await flush();

    expect(onchange).not.toHaveBeenCalled();
    const errDup = target.querySelector('[data-testid="cloze-alt-error"]');
    expect(errDup).not.toBeNull();
    expect(errDup!.textContent).toContain('备选答案已存在');
  });

  it('shows warning on malformed JSON and requires explicit confirmation to clear and reconfigure', async () => {
    const onchange = vi.fn();
    const badJson = '<html>invalid json</html>';
    const { target } = renderEditor({
      textFieldValue: '{{c1::Paris}}',
      metaValue: badJson,
      onchange,
    });
    await flush();

    const banner = target.querySelector('[data-testid="cloze-alt-malformed-banner"]');
    expect(banner).not.toBeNull();
    expect(banner!.textContent).toContain('元数据包含无法解析的内容');

    // Click Clear metadata & reconfigure button
    const resetBtn = target.querySelector('[data-testid="cloze-alt-reset-btn"]') as HTMLButtonElement;
    expect(resetBtn).not.toBeNull();
    resetBtn.click();
    await flush();

    // Confirmation dialog opens in document.body
    const dialog = document.body.querySelector('[data-testid="cloze-alt-reset-dialog"]');
    expect(dialog).not.toBeNull();
    expect(dialog!.textContent).toContain('此操作将永久清空该笔记的全部原始元数据');

    // 1. Cancel
    const cancelBtn = document.body.querySelector('[data-testid="cloze-alt-reset-cancel"]') as HTMLButtonElement;
    expect(cancelBtn).not.toBeNull();
    cancelBtn.click();
    await flush();
    expect(onchange).not.toHaveBeenCalled();

    // 2. Open dialog again and confirm
    resetBtn.click();
    await flush();
    const confirmBtn = document.body.querySelector('[data-testid="cloze-alt-reset-confirm"]') as HTMLButtonElement;
    expect(confirmBtn).not.toBeNull();
    confirmBtn.click();
    await flush();

    expect(onchange).toHaveBeenCalled();
    const serialized = JSON.parse(onchange.mock.calls.at(-1)![0]);
    expect(serialized.cloze_accept).toEqual({});
  });

  it('resets only a malformed capability while preserving other complete JSON values', async () => {
    const onchange = vi.fn();
    const { target } = renderEditor({
      textFieldValue: '{{c1::Paris}}',
      metaValue: JSON.stringify({ future_key: { nested: [null, false, { x: 1 }] }, cloze_accept: null }),
      onchange,
    });
    await flush();
    (target.querySelector('[data-testid="cloze-alt-reset-btn"]') as HTMLButtonElement).click();
    await flush();
    expect(document.body.querySelector('[data-testid="cloze-alt-reset-dialog"]')?.textContent).toContain('其他元数据会保留');
    (document.body.querySelector('[data-testid="cloze-alt-reset-confirm"]') as HTMLButtonElement).click();
    await flush();
    expect(JSON.parse(onchange.mock.calls.at(-1)![0])).toEqual({ future_key: { nested: [null, false, { x: 1 }] }, cloze_accept: {} });
  });

  it('displays unlinked alternatives and supports reassociating and deleting', async () => {
    const onchange = vi.fn();
    const rawMeta = JSON.stringify({
      cloze_accept: {
        '1': {
          Paris: ['City of Light'],
          London: ['Capital of UK'], // Unlinked because text only has Paris
        },
      },
    });

    const { target } = renderEditor({
      textFieldValue: '{{c1::Paris}} is capital.',
      metaValue: rawMeta,
      onchange,
    });
    await flush();

    const unlinkedSection = target.querySelector('[data-testid="cloze-alt-unlinked-section"]');
    expect(unlinkedSection).not.toBeNull();
    expect(unlinkedSection!.textContent).toContain('London');
    expect(unlinkedSection!.textContent).toContain('Capital of UK');

    // Test reassociation
    const trigger = target.querySelector('[data-testid="cloze-alt-reassociate-select-0"]') as HTMLElement;
    expect(trigger).not.toBeNull();
    trigger.click();
    await flush();

    const option = Array.from(document.body.querySelectorAll('[role="option"]')).find(
      (el) => el.textContent?.trim() === 'c1: Paris'
    ) as HTMLElement;
    expect(option, 'reassociate option c1: Paris').not.toBeNull();
    option.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true }));
    option.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, cancelable: true }));
    await flush();

    expect(onchange).toHaveBeenCalled();
    const updated = JSON.parse(onchange.mock.calls.at(-1)![0]);
    expect(updated.cloze_accept['1']['Paris']).toEqual(['City of Light', 'Capital of UK']);
    expect(updated.cloze_accept['1']['London']).toBeUndefined();

    // Now test delete
    const deleteOnChange = vi.fn();
    const { target: target2 } = renderEditor({
      textFieldValue: '{{c1::Paris}} is capital.',
      metaValue: rawMeta,
      onchange: deleteOnChange,
    });
    await flush();

    const deleteBtn = target2.querySelector('[data-testid="cloze-alt-unlinked-delete-0"]') as HTMLButtonElement;
    expect(deleteBtn).not.toBeNull();
    deleteBtn.click();
    await flush();
    const afterDelete = JSON.parse(deleteOnChange.mock.calls.at(-1)![0]);
    expect(afterDelete.cloze_accept['1'].London).toBeUndefined();
    expect(afterDelete.cloze_accept['1'].Paris).toEqual(['City of Light']);
  });

  it('reactively rederives unlinked alternatives when note text changes without metadata change', async () => {
    setLocale('zh-CN');
    target = document.createElement('div');
    document.body.appendChild(target);

    // Initial note text has {{c1::Paris}}, and metadata has Paris: ["City of Light"]
    const rawMeta = JSON.stringify({
      cloze_accept: {
        '1': {
          Paris: ['City of Light'],
        },
      },
    });

    instance = mount(ClozeAlternativesHarness, {
      target,
      props: {
        textFieldValue: '{{c1::Paris}} is capital.',
        metaValue: rawMeta,
      },
    });
    await flush();

    // Currently Paris is active, so unlinked section should NOT exist
    expect(target.querySelector('[data-testid="cloze-alt-unlinked-section"]')).toBeNull();

    // Now user modifies text to {{c1::Rome}} without changing metadata
    const changeBtn = target.querySelector('[data-testid="harness-change-text"]') as HTMLButtonElement;
    expect(changeBtn).not.toBeNull();
    changeBtn.click();
    await flush();

    // Unlinked section must now reactively appear, holding Paris!
    const unlinkedSection = target.querySelector('[data-testid="cloze-alt-unlinked-section"]');
    expect(unlinkedSection).not.toBeNull();
    expect(unlinkedSection!.textContent).toContain('Paris');
    expect(unlinkedSection!.textContent).toContain('City of Light');
  });

  it('displays warning on invalid mixed-array row, preserves mixed array on sibling edit, and repairs on click', async () => {
    const onchange = vi.fn();
    const rawMeta = JSON.stringify({
      cloze_accept: {
        '1': {
          Paris: ['City of Light', 12345], // Mixed array (invalid)
          Rome: ['Eternal City'],          // Valid sibling
        },
      },
    });

    const { target } = renderEditor({
      textFieldValue: '{{c1::Paris}} and {{c1::Rome}}.',
      metaValue: rawMeta,
      onchange,
    });
    await flush();

    // 1. Invalid entry warning is displayed on Paris
    const warn = target.querySelector('[data-testid="cloze-alt-invalid-entry-warning"]');
    expect(warn).not.toBeNull();
    expect(warn!.textContent).toContain('该条目的备选答案数据包含非文本项');

    // 2. Edit valid sibling Rome by adding "Roma"
    const romeInput = target.querySelector('input[data-testid="cloze-alt-input-1-1"]') as HTMLInputElement;
    const romeAddBtn = target.querySelector('button[data-testid="cloze-alt-add-btn-1-1"]') as HTMLButtonElement;
    expect(romeInput).not.toBeNull();
    expect(romeAddBtn).not.toBeNull();

    romeInput.value = 'Roma';
    romeInput.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    romeAddBtn.click();
    await flush();

    expect(onchange).toHaveBeenCalled();
    const serializedSibling = JSON.parse(onchange.mock.calls.at(-1)![0]);
    // Sibling Rome is updated
    expect(serializedSibling.cloze_accept['1']['Rome']).toEqual(['Eternal City', 'Roma']);
    // Un-repaired Paris mixed array [City of Light, 12345] is PRESERVED untouched!
    expect(serializedSibling.cloze_accept['1']['Paris']).toEqual(['City of Light', 12345]);

    // 3. Now click "修复此项" on Paris
    const repairBtn = target.querySelector('[data-testid="cloze-alt-repair-entry-btn"]') as HTMLButtonElement;
    expect(repairBtn).not.toBeNull();
    repairBtn.click();
    await flush();

    expect(onchange).toHaveBeenCalledTimes(2);
    const serializedRepaired = JSON.parse(onchange.mock.calls.at(-1)![0]);
    // Paris is now repaired to clean string array!
    expect(serializedRepaired.cloze_accept['1']['Paris']).toEqual(['City of Light']);
    expect(target.querySelector('[data-testid="cloze-alt-invalid-entry-warning"]')).toBeNull();
  });
});
