/**
 * Markdown emphasis toggling for a plain text control.
 *
 * The editor keeps a field's value as Markdown source, so a formatting action edits that source
 * instead of replacing the control with a rich-text surface. This module holds only the text
 * transformation: it takes the current text, the selection offsets and one marker, and returns the
 * new text with the offsets the caret or selection must take afterwards. It never touches the DOM,
 * which is what makes every case here testable without a browser.
 *
 * CommonMark reads `*` as emphasis and `~~` as strikethrough (GitHub Flavored Markdown); `**` is
 * strong emphasis. Those three are the whole vocabulary the toolbar offers.
 */

/** The three emphasis markers the toolbar writes. */
export type MarkdownMarker = '**' | '*' | '~~';

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
