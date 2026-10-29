/**
 * Markdown source edits for a plain text control.
 *
 * The editor keeps a field's value as Markdown source, so a formatting action edits that source
 * instead of replacing the control with a rich-text surface. This module holds only the text
 * transformation: it takes the current text and the selection offsets, and returns the new text
 * with the offsets the caret or selection must take afterwards. It never touches the DOM, which is
 * what makes every case here testable without a browser.
 *
 * The vocabulary is what the card renderer keeps (GitHub Flavored Markdown, sanitized): inline
 * emphasis, strikethrough, code and links, plus quotes, lists, fenced code and tables as blocks.
 * Headings are left out on purpose: the card sanitizer strips them, so a heading button would write
 * markup that never shows.
 */

/** The inline markers the toolbar toggles around a selection. */
export type MarkdownMarker = '**' | '*' | '~~' | '`';

/** The result of one toggle: the new field text and where the selection sits inside it. */
export interface MarkdownEdit {
  text: string;
  start: number;
  end: number;
}

/** The keyboard state that decides a formatting shortcut; kept free of DOM event classes. */
export interface ShortcutInput {
  key: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  shiftKey?: boolean;
  altKey?: boolean;
}

/** Clamps one offset into `[0, length]`, turning a NaN or a fractional value from a hostile caller into a usable index. */
function clampOffset(value: number, length: number): number {
  if (!Number.isFinite(value)) return 0;
  return Math.min(Math.max(0, Math.trunc(value)), length);
}

/** Counts consecutive `char` characters immediately before `index`. */
function runBefore(text: string, index: number, char: string): number {
  let width = 0;
  for (let cursor = index - 1; cursor >= 0 && text[cursor] === char; cursor -= 1) width += 1;
  return width;
}

/** Counts consecutive `char` characters starting at `index`. */
function runAfter(text: string, index: number, char: string): number {
  let width = 0;
  for (let cursor = index; cursor < text.length && text[cursor] === char; cursor += 1) width += 1;
  return width;
}

/**
 * Whether a run of marker characters can stand as a pair for `marker`.
 *
 * A lone `*` is only italic when it stands alone: inside a `**` run it is half of a bold
 * marker, and treating it as italic would replace bold instead of adding italic. A `**` pair
 * is the two stars touching the content, so a three-star run holds bold plus italic and
 * removing the bold pair leaves the italic star. Strikethrough is always exactly `~~`.
 */
function isPairRun(width: number, marker: MarkdownMarker): boolean {
  if (marker === '*') return width === 1;
  if (marker === '**') return width === 2 || width === 3;
  // A code span is one backtick on each side here; a run of three is a code fence, not a span.
  if (marker === '`') return width === 1;
  return width === 2;
}

/** Whether a marker pair sits immediately outside the selected range, with both adjacent runs accepted as that marker. */
function isWrappedOutside(text: string, start: number, end: number, marker: MarkdownMarker): boolean {
  const char = marker.charAt(0);
  return isPairRun(runBefore(text, start, char), marker) && isPairRun(runAfter(text, end, char), marker);
}

/** Whether the selected text's leading and trailing marker runs both stand as a pair for the marker. */
function isWrappedInside(selected: string, marker: MarkdownMarker): boolean {
  const char = marker.charAt(0);
  return (
    selected.length >= marker.length * 2 &&
    isPairRun(runAfter(selected, 0, char), marker) &&
    isPairRun(runBefore(selected, selected.length, char), marker)
  );
}

/**
 * Toggles `marker` around the current selection.
 *
 * - A selection whose text already includes both markers loses them; the inner text stays selected.
 * - A selection flanked by the marker pair loses that pair; the selected text stays selected.
 * - A collapsed caret flanked by the pair loses it and stays in place, so pressing the same
 *   shortcut twice returns to the original text instead of growing the marker count.
 * - Every other selection is wrapped, and the originally selected text stays selected inside the
 *   new pair so a second press removes it again.
 * - A collapsed caret inserts the empty pair and leaves the caret between the two markers.
 *
 * The offsets are clamped into the text first, so a stale caret from before a model update cannot
 * slice outside the string. Reversed offsets are treated as a normal selection.
 */
export function toggleMarkdown(
  text: string,
  start: number,
  end: number,
  marker: MarkdownMarker
): MarkdownEdit {
  const first = clampOffset(Math.min(start, end), text.length);
  const last = clampOffset(Math.max(start, end), text.length);
  const before = text.slice(0, first);
  const selected = text.slice(first, last);
  const after = text.slice(last);

  if (selected === '') {
    const char = marker.charAt(0);
    if (isPairRun(runBefore(text, first, char), marker) && isPairRun(runAfter(text, first, char), marker)) {
      const caret = first - marker.length;
      return {
        text: before.slice(0, -marker.length) + after.slice(marker.length),
        start: caret,
        end: caret,
      };
    }
    const caret = first + marker.length;
    return { text: `${before}${marker}${marker}${after}`, start: caret, end: caret };
  }

  if (isWrappedInside(selected, marker)) {
    const inner = selected.slice(marker.length, selected.length - marker.length);
    return { text: `${before}${inner}${after}`, start: first, end: first + inner.length };
  }

  if (isWrappedOutside(text, first, last, marker)) {
    const nextStart = first - marker.length;
    return {
      text: `${before.slice(0, -marker.length)}${selected}${after.slice(marker.length)}`,
      start: nextStart,
      end: nextStart + selected.length,
    };
  }

  return {
    text: `${before}${marker}${selected}${marker}${after}`,
    start: first + marker.length,
    end: last + marker.length,
  };
}

/**
 * Maps one key press to a marker, or null when it is not a formatting shortcut.
 *
 * Ctrl on every platform and Cmd on macOS are both accepted, so the same binding works whether the
 * browser reports `ctrlKey` or `metaKey`. Shift is reserved: `Ctrl/Cmd+Shift+X` is the
 * strikethrough binding, and adding Shift to bold or italic would collide with other browser
 * shortcuts while doing nothing a writer asked for. Alt is never part of these bindings.
 */
export function markerForShortcut(input: ShortcutInput): MarkdownMarker | null {
  if (!(input.ctrlKey === true || input.metaKey === true) || input.altKey === true) return null;
  if (input.shiftKey === true) {
    return input.key.toLowerCase() === 'x' ? '~~' : null;
  }
  switch (input.key.toLowerCase()) {
    case 'b':
      return '**';
    case 'i':
      return '*';
    default:
      return null;
  }
}

/** Offsets clamped into the text and put in order, so every edit below reads one selection shape. */
function orderedRange(text: string, start: number, end: number): [number, number] {
  return [clampOffset(Math.min(start, end), text.length), clampOffset(Math.max(start, end), text.length)];
}

/**
 * The separators a block needs so it stands on its own lines with a blank line on each side.
 *
 * A table only starts at a line of its own, and a blank line keeps the paragraph before it from
 * swallowing its header row; the same spacing keeps a fenced block visibly apart in the source. The
 * start and the end of the field need no separator.
 */
function blockPadding(before: string, after: string): [string, string] {
  const lead = before === '' || before.endsWith('\n\n') ? '' : before.endsWith('\n') ? '\n' : '\n\n';
  const trail = after === '' || after.startsWith('\n\n') ? '' : after.startsWith('\n') ? '\n' : '\n\n';
  return [lead, trail];
}

/** The line-level blocks the toolbar toggles: a quote, a bulleted list and a numbered list. */
export type LinePrefix = 'quote' | 'bullet' | 'ordered';

const PREFIX_PATTERNS: Record<LinePrefix, RegExp> = {
  quote: /^> ?/,
  bullet: /^[-*+] /,
  ordered: /^\d+[.)] /,
};

/**
 * Toggles a line prefix on every line the selection touches.
 *
 * - When every non-empty line already carries the prefix, the prefix is removed from all of them.
 * - Otherwise each non-empty line gets it. A line that is a list item of the other kind is
 *   converted instead of being double-marked, so a bulleted list turns into a numbered one.
 * - A numbered list counts only the non-empty lines, from 1.
 * - An empty line inside the selection stays empty, so a paragraph break is not turned into an
 *   empty item; a selection that is one empty line gets the prefix, so the writer can start typing.
 *
 * A selection that ends at the very start of a line does not include that line: that is where a
 * selection made by dragging over whole lines ends. A collapsed caret stays on the same character;
 * a selection afterwards covers the edited lines.
 */
export function toggleLinePrefix(text: string, start: number, end: number, kind: LinePrefix): MarkdownEdit {
  const [first, last] = orderedRange(text, start, end);
  const effectiveLast = last > first && text[last - 1] === '\n' ? last - 1 : last;
  const blockStart = text.lastIndexOf('\n', first - 1) + 1;
  const newline = text.indexOf('\n', effectiveLast);
  const blockEnd = newline === -1 ? text.length : newline;
  const lines = text.slice(blockStart, blockEnd).split('\n');

  const pattern = PREFIX_PATTERNS[kind];
  const filled = lines.filter((line) => line.trim() !== '');
  const remove = filled.length > 0 && filled.every((line) => pattern.test(line));

  let count = 0;
  const edited = lines.map((line) => {
    if (remove) return line.replace(pattern, '');
    if (line.trim() === '' && filled.length > 0) return line;
    let body = line.replace(pattern, '');
    // Converting between the two list kinds drops the other marker rather than nesting it.
    if (kind === 'bullet') body = body.replace(PREFIX_PATTERNS.ordered, '');
    if (kind === 'ordered') body = body.replace(PREFIX_PATTERNS.bullet, '');
    count += 1;
    const prefix = kind === 'quote' ? '> ' : kind === 'bullet' ? '- ' : `${count}. `;
    return prefix + body;
  });

  const block = edited.join('\n');
  const result = text.slice(0, blockStart) + block + text.slice(blockEnd);
  if (first === last) {
    // One line only: the caret moves by however much its line grew or shrank, but never in front of the line.
    const caret = Math.max(blockStart, first + (edited[0] ?? '').length - (lines[0] ?? '').length);
    return { text: result, start: caret, end: caret };
  }
  return { text: result, start: blockStart, end: blockStart + block.length };
}

/**
 * Wraps the selection in a fenced code block, or inserts an empty one at the caret.
 *
 * The fences go on lines of their own, padded off the surrounding text. The selected text stays
 * selected inside the fences; an empty block leaves the caret on the empty line between them. A
 * trailing newline in the selection is kept outside the closing fence, so the block does not end
 * with a blank code line.
 */
export function insertCodeBlock(text: string, start: number, end: number): MarkdownEdit {
  const [first, last] = orderedRange(text, start, end);
  const before = text.slice(0, first);
  const selected = text.slice(first, last);
  const inner = selected.replace(/\n$/, '');
  const after = selected.slice(inner.length) + text.slice(last);
  const [lead, trail] = blockPadding(before, after);
  const open = `${lead}\`\`\`\n`;
  const block = `${open}${inner}\n\`\`\`${trail}`;
  const innerStart = first + open.length;
  return { text: before + block + after, start: innerStart, end: innerStart + inner.length };
}

/**
 * Inserts a two-column table after the selection, keeping the selected text as it was.
 *
 * The header cells come from the caller, because they are words the writer sees and edits in the
 * active language. The first header cell is selected afterwards, so typing replaces it.
 */
export function insertTable(text: string, start: number, end: number, headers: readonly [string, string]): MarkdownEdit {
  const [, last] = orderedRange(text, start, end);
  const before = text.slice(0, last);
  const after = text.slice(last);
  const [lead, trail] = blockPadding(before, after);
  const block = `| ${headers[0]} | ${headers[1]} |\n| --- | --- |\n|  |  |`;
  const cell = last + lead.length + 2;
  return { text: before + lead + block + trail + after, start: cell, end: cell + headers[0].length };
}

/**
 * Turns the selection into a link.
 *
 * - A selection that is a web address becomes the link target, and the caret waits in the empty
 *   link text.
 * - Any other selection becomes the link text, and the placeholder target `https://` is selected so
 *   pasting an address replaces it.
 * - A collapsed caret inserts an empty link and waits in its text.
 */
export function insertLink(text: string, start: number, end: number): MarkdownEdit {
  const [first, last] = orderedRange(text, start, end);
  const before = text.slice(0, first);
  const selected = text.slice(first, last);
  const after = text.slice(last);
  if (/^https?:\/\/\S+$/i.test(selected)) {
    return { text: `${before}[](${selected})${after}`, start: first + 1, end: first + 1 };
  }
  const target = 'https://';
  const source = `[${selected}](${target})`;
  if (selected === '') return { text: before + source + after, start: first + 1, end: first + 1 };
  const targetStart = first + selected.length + 3;
  return { text: before + source + after, start: targetStart, end: targetStart + target.length };
}
