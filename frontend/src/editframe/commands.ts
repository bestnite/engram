/**
 * DOM edits the rich-text frame performs for one toolbar command or keyboard shortcut.
 *
 * `document.execCommand` is deprecated and unimplemented in the test environment, so every
 * command here is a small explicit range edit: it wraps, unwraps or replaces the current
 * selection and leaves the resulting DOM for the runtime to serialise. The functions take the
 * document and the editing root instead of reading globals, which is what lets the client tests
 * drive them directly.
 *
 * The element shapes follow Anki's editor: `<b>`, `<i>`, `<u>`, `<sup>`, `<sub>`, a `<span>`
 * carrying `color` or `background-color` for coloured text, `<ul>`/`<ol>` lists, and a block
 * element whose `text-align` or `margin-left` carries alignment and indentation.
 */

export type InlineFormat = 'bold' | 'italic' | 'underline' | 'superscript' | 'subscript';

export type AlignValue = 'left' | 'center' | 'right' | 'justify';

const INLINE_TAGS: Record<InlineFormat, string> = {
  bold: 'b',
  italic: 'i',
  underline: 'u',
  superscript: 'sup',
  subscript: 'sub',
};

/** Inline elements that count as formatting for "clear formatting" and for the paste filter. */
const FORMATTING_TAGS = new Set(['B', 'STRONG', 'I', 'EM', 'U', 'S', 'SUP', 'SUB', 'SPAN', 'FONT', 'A', 'CODE', 'PRE']);

/** Block elements a caret can sit in; alignment and indentation write to the nearest one. */
const BLOCK_TAGS = new Set(['DIV', 'P', 'LI', 'BLOCKQUOTE', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'PRE', 'TD', 'TH']);

/** The tags a pasted HTML fragment may keep when the formatting survives the paste. */
const PASTE_ALLOWED_TAGS = new Set([
  'B', 'STRONG', 'I', 'EM', 'U', 'S', 'SUP', 'SUB', 'SPAN', 'FONT', 'A', 'IMG', 'BR',
  'UL', 'OL', 'LI', 'DIV', 'P', 'BLOCKQUOTE', 'PRE', 'CODE', 'TABLE', 'THEAD', 'TBODY',
  'TR', 'TD', 'TH', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6',
]);

/** Style properties the paste filter keeps; everything else is dropped so pasted layout cannot leak in. */
const PASTE_STYLE_PROPERTIES = ['color', 'background-color', 'text-align', 'margin-left', 'font-weight', 'font-style', 'text-decoration'];

/** A selection position: the range a command must act on. */
export interface EditingContext {
  doc: Document;
  root: HTMLElement;
}

/** The caret at the end of the root, used when the document selection is outside the editor. */
export function endRange(ctx: EditingContext): Range {
  const range = ctx.doc.createRange();
  range.selectNodeContents(ctx.root);
  range.collapse(false);
  return range;
}

/** Whether a range is inside the editing root. */
export function rangeInRoot(ctx: EditingContext, range: Range): boolean {
  return ctx.root.contains(range.startContainer) && ctx.root.contains(range.endContainer);
}

/**
 * The selection to edit: the document selection when it is inside the root, otherwise the caret at
 * the end. A toolbar click moves focus out of the frame on some browsers, and a paste handler can
 * run after the selection moved; both must still edit the field instead of throwing.
 */
export function currentRange(ctx: EditingContext, fallback?: Range | null): Range {
  const selection = ctx.doc.getSelection();
  if (selection && selection.rangeCount > 0) {
    const range = selection.getRangeAt(0);
    if (rangeInRoot(ctx, range)) return range;
  }
  if (fallback && rangeInRoot(ctx, fallback)) return fallback.cloneRange();
  return endRange(ctx);
}

/** Puts the caret or selection back into the document, so keyboard typing continues in the field. */
export function selectRange(ctx: EditingContext, range: Range): void {
  const selection = ctx.doc.getSelection();
  if (!selection) return;
  selection.removeAllRanges();
  selection.addRange(range);
  ctx.root.focus();
}

/** The nearest ancestor with one of the given tag names, stopping at the editing root. */
export function closestWithin(node: Node, tags: ReadonlySet<string>, root: HTMLElement): HTMLElement | null {
  let current: Node | null = node;
  while (current && current !== root) {
    if (current instanceof Element && tags.has(current.tagName)) return current as HTMLElement;
    current = current.parentNode;
  }
  return null;
}

/** The nearest ancestor with a given tag name, stopping at the editing root. */
export function closestTag(node: Node, tag: string, root: HTMLElement): HTMLElement | null {
  return closestWithin(node, new Set([tag.toUpperCase()]), root);
}

/** Replaces an element with its children, keeping the content and dropping the wrapper. */
export function unwrap(element: Element): void {
  const parent = element.parentNode;
  if (!parent) return;
  while (element.firstChild) parent.insertBefore(element.firstChild, element);
  parent.removeChild(element);
}

/** Wraps the selected content in a new element; a collapsed caret gets an empty element around it. */
function surroundRange(ctx: EditingContext, range: Range, element: HTMLElement): void {
  if (range.collapsed) {
    range.insertNode(element);
    const caret = ctx.doc.createRange();
    caret.setStart(element, 0);
    caret.collapse(true);
    selectRange(ctx, caret);
    return;
  }
  const fragment = range.extractContents();
  element.appendChild(fragment);
  range.insertNode(element);
  const selected = ctx.doc.createRange();
  selected.selectNodeContents(element);
  selectRange(ctx, selected);
}

/** Toggles one inline element around the selection. */
export function toggleInline(ctx: EditingContext, format: InlineFormat, fallback?: Range | null): void {
  const tag = INLINE_TAGS[format];
  const range = currentRange(ctx, fallback);
  const existing = closestTag(range.startContainer, tag, ctx.root);
  if (existing) {
    unwrap(existing);
    return;
  }
  surroundRange(ctx, range, ctx.doc.createElement(tag));
}

/**
 * Applies a styled span to the selection, or updates the span that already carries the property.
 * Pressing the same colour again removes it, so the button is a toggle like the inline ones.
 */
export function applyStyle(ctx: EditingContext, property: 'color' | 'background-color', value: string, fallback?: Range | null): void {
  if (!/^#[0-9a-f]{6}$/i.test(value)) return;
  const range = currentRange(ctx, fallback);
  const span = closestTag(range.startContainer, 'span', ctx.root);
  if (span && span.style.getPropertyValue(property)) {
    if (span.style.getPropertyValue(property).toLowerCase() === value.toLowerCase()) {
      span.style.removeProperty(property);
      if (span.getAttribute('style') === '') span.removeAttribute('style');
      if (span.attributes.length === 0) unwrap(span);
    } else {
      span.style.setProperty(property, value);
    }
    return;
  }
  const wrapper = ctx.doc.createElement('span');
  wrapper.style.setProperty(property, value);
  surroundRange(ctx, range, wrapper);
}

/** Strips every formatting element that intersects the selection, keeping the text. */
export function clearFormatting(ctx: EditingContext, fallback?: Range | null): void {
  const range = currentRange(ctx, fallback);
  // Unwrap the formatting the selection starts and ends in first: extracting only the selected text
  // would leave the wrapper in place and the text would return into it unchanged.
  for (const boundary of [range.startContainer, range.endContainer]) {
    let wrapper = closestWithin(boundary, FORMATTING_TAGS, ctx.root);
    while (wrapper) {
      const parent = wrapper.parentNode;
      unwrap(wrapper);
      wrapper = parent ? closestWithin(parent, FORMATTING_TAGS, ctx.root) : null;
    }
  }
  if (range.collapsed) return;
  const fragment = range.extractContents();
  stripFormatting(fragment);
  range.insertNode(fragment);
}

/** Removes formatting elements inside a fragment, keeping text, blocks and media. */
function stripFormatting(fragment: DocumentFragment | Element): void {
  for (const element of Array.from(fragment.querySelectorAll('*'))) {
    if (!element.isConnected && !fragment.contains(element)) continue;
    if (FORMATTING_TAGS.has(element.tagName)) {
      const parent = element.parentNode;
      if (!parent) continue;
      while (element.firstChild) parent.insertBefore(element.firstChild, element);
      parent.removeChild(element);
    } else {
      for (const attribute of Array.from(element.attributes)) {
        if (attribute.name === 'style' || attribute.name.startsWith('on')) element.removeAttribute(attribute.name);
      }
    }
  }
}

/** Turns the selection into a list, removes it, or switches an existing list to the other kind. */
export function toggleList(ctx: EditingContext, kind: 'ul' | 'ol', fallback?: Range | null): void {
  const range = currentRange(ctx, fallback);
  const list = closestWithin(range.startContainer, new Set(['UL', 'OL']), ctx.root);
  if (list) {
    if (list.tagName.toLowerCase() === kind) {
      unwrapList(ctx, list);
    } else {
      const replacement = ctx.doc.createElement(kind);
      while (list.firstChild) replacement.appendChild(list.firstChild);
      list.replaceWith(replacement);
    }
    return;
  }
  const item = ctx.doc.createElement('li');
  if (range.collapsed) {
    item.appendChild(ctx.doc.createElement('br'));
  } else {
    item.appendChild(range.extractContents());
  }
  const newList = ctx.doc.createElement(kind);
  newList.appendChild(item);
  range.insertNode(newList);
}

/** Replaces a list with its items' content, separated by line breaks, as a list toggle-off does. */
function unwrapList(ctx: EditingContext, list: HTMLElement): void {
  const fragment = ctx.doc.createDocumentFragment();
  Array.from(list.children).forEach((child, index) => {
    if (index > 0) fragment.appendChild(ctx.doc.createElement('br'));
    while (child.firstChild) fragment.appendChild(child.firstChild);
  });
  list.replaceWith(fragment);
}

/** The block element a command writes alignment or indentation to, wrapping loose content when needed. */
function blockFor(ctx: EditingContext, range: Range): HTMLElement {
  const block = closestWithin(range.startContainer, BLOCK_TAGS, ctx.root);
  if (block && block !== ctx.root) return block;
  for (const child of Array.from(ctx.root.children)) {
    if (child instanceof HTMLElement && BLOCK_TAGS.has(child.tagName) && child.contains(range.startContainer)) {
      return child;
    }
  }
  const wrapper = ctx.doc.createElement('div');
  while (ctx.root.firstChild) wrapper.appendChild(ctx.root.firstChild);
  ctx.root.appendChild(wrapper);
  return wrapper;
}

/** Sets the block's text alignment; `left` removes the property instead of writing the default. */
export function alignBlock(ctx: EditingContext, value: AlignValue, fallback?: Range | null): void {
  const block = blockFor(ctx, currentRange(ctx, fallback));
  if (value === 'left') {
    block.style.removeProperty('text-align');
    if (block.getAttribute('style') === '') block.removeAttribute('style');
    return;
  }
  block.style.textAlign = value;
}

/** Moves the block's left margin by one step; the step matches Anki's editor indent. */
export function indentBlock(ctx: EditingContext, delta: 1 | -1, fallback?: Range | null): void {
  const block = blockFor(ctx, currentRange(ctx, fallback));
  const current = Number.parseFloat(block.style.marginLeft) || 0;
  const next = Math.min(400, Math.max(0, current + delta * 40));
  if (next === 0) {
    block.style.removeProperty('margin-left');
    if (block.getAttribute('style') === '') block.removeAttribute('style');
    return;
  }
  block.style.marginLeft = `${next}px`;
}

/** Inserts an HTML fragment at the selection, replacing selected content. */
export function insertHtml(ctx: EditingContext, html: string, fallback?: Range | null): void {
  const range = currentRange(ctx, fallback);
  range.deleteContents();
  const template = ctx.doc.createElement('template');
  template.innerHTML = html;
  const fragment = template.content.cloneNode(true) as DocumentFragment;
  const last = fragment.lastChild;
  range.insertNode(fragment);
  const after = ctx.doc.createRange();
  if (last) after.setStartAfter(last);
  else after.setStart(range.endContainer, range.endOffset);
  after.collapse(true);
  selectRange(ctx, after);
}

/**
 * Wraps the selection in literal marker text (`\(`/`\)`, `{{c1::`/`}}`).
 *
 * The markers are text nodes, not elements, because the field value is a string the server and
 * Anki parse as source: the selected markup stays inside the markers unchanged.
 */
export function wrapWithMarkers(ctx: EditingContext, front: string, back: string, fallback?: Range | null): void {
  const range = currentRange(ctx, fallback);
  if (range.collapsed) {
    const text = ctx.doc.createTextNode(`${front}${back}`);
    range.insertNode(text);
    const caret = ctx.doc.createRange();
    caret.setStart(text, front.length);
    caret.collapse(true);
    selectRange(ctx, caret);
    return;
  }
  const endRange = range.cloneRange();
  endRange.collapse(false);
  endRange.insertNode(ctx.doc.createTextNode(back));
  const startRange = range.cloneRange();
  startRange.collapse(true);
  startRange.insertNode(ctx.doc.createTextNode(front));
  selectRange(ctx, range);
}

/** Highest `{{cN::` number in the field, or 0 when the field has no cloze. */
export function highestClozeNumber(html: string): number {
  let highest = 0;
  const pattern = /\{\{c(\d+)::/g;
  let match: RegExpExecArray | null;
  while ((match = pattern.exec(html)) !== null) {
    const value = Number.parseInt(match[1] ?? '0', 10);
    if (Number.isFinite(value)) highest = Math.max(highest, value);
  }
  return highest;
}

/**
 * The cloze number a new deletion gets. With `increment` it is one past the highest existing
 * number; without it, the highest existing number is reused. Both start at 1 on an empty field.
 */
export function clozeNumber(html: string, increment: boolean): number {
  const highest = highestClozeNumber(html);
  return Math.max(1, increment ? highest + 1 : highest);
}

/** Escapes text for use as HTML text content. */
export function escapeHtml(text: string): string {
  return text
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;');
}

/** Escapes text for use inside a double-quoted attribute. */
export function escapeAttribute(text: string): string {
  return escapeHtml(text);
}

/** Plain text of an HTML string with line breaks for block boundaries. */
export function htmlToPlainText(ctx: EditingContext, html: string): string {
  const template = ctx.doc.createElement('template');
  template.innerHTML = html;
  const parts: string[] = [];
  const walk = (node: Node): void => {
    if (node.nodeType === 3) {
      parts.push(node.textContent ?? '');
      return;
    }
    if (!(node instanceof Element)) {
      node.childNodes.forEach(walk);
      return;
    }
    if (node.tagName === 'BR') {
      parts.push('\n');
      return;
    }
    const block = BLOCK_TAGS.has(node.tagName);
    node.childNodes.forEach(walk);
    if (block && !parts.at(-1)?.endsWith('\n')) parts.push('\n');
  };
  template.content.childNodes.forEach(walk);
  return parts.join('').replace(/\n+$/, '');
}

/** HTML for plain text typed into a plain-text field: escaped text with line breaks. */
export function plainTextToHtml(text: string): string {
  return escapeHtml(text).replaceAll('\n', '<br>');
}

/**
 * Filters a pasted HTML fragment.
 *
 * A paste keeps inline formatting, lists and tables, but drops scripts, styles the editor does not
 * produce, event handlers and non-http links: the editing frame runs with scripts enabled, so pasted
 * markup must not carry behaviour into it. Holding Shift strips the formatting and keeps the text
 * and line breaks only. Keeping markup is Anki 26.09.3's default: its editor reads
 * `PASTE_STRIPS_FORMATTING` and inverts it while Shift is held
 * (`ts/routes/editor/rich-text-input/data-transfer.ts`), and `rslib/src/config/bool.rs` leaves that
 * key out of the list that defaults to true.
 */
export function pasteHtml(ctx: EditingContext, html: string, keepFormatting: boolean): string {
  const template = ctx.doc.createElement('template');
  template.innerHTML = html;
  if (!keepFormatting) return plainTextToHtml(htmlToPlainText(ctx, html));
  filterElement(ctx, template.content);
  return template.innerHTML;
}

/** Recursively rewrites a pasted fragment in place. */
function filterElement(ctx: EditingContext, node: ParentNode): void {
  for (const element of Array.from(node.querySelectorAll('*'))) {
    if (!PASTE_ALLOWED_TAGS.has(element.tagName)) {
      unwrap(element);
      continue;
    }
    for (const attribute of Array.from(element.attributes)) {
      const name = attribute.name.toLowerCase();
      if (name.startsWith('on') || name === 'class' || name === 'id') {
        element.removeAttribute(attribute.name);
        continue;
      }
      if (name === 'style') {
        const kept: string[] = [];
        for (const property of PASTE_STYLE_PROPERTIES) {
          const value = (element as HTMLElement).style.getPropertyValue(property);
          if (value) kept.push(`${property}: ${value}`);
        }
        if (kept.length > 0) element.setAttribute('style', kept.join('; '));
        else element.removeAttribute('style');
        continue;
      }
      if (name === 'href' || name === 'src') {
        if (!/^https?:\/\//i.test(attribute.value)) element.removeAttribute(attribute.name);
        continue;
      }
      if (name !== 'color') element.removeAttribute(attribute.name);
    }
  }
  void ctx;
}
