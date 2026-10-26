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

/** Whether the marker pair sits immediately outside the selected range. */
function isWrappedOutside(text: string, start: number, end: number, marker: string): boolean {
  return (
    start >= marker.length &&
    end + marker.length <= text.length &&
    text.slice(start - marker.length, start) === marker &&
    text.slice(end, end + marker.length) === marker
  );
}

/** Whether the selected text itself begins and ends with the marker, i.e. the pair is inside the selection. */
function isWrappedInside(selected: string, marker: string): boolean {
  return (
    selected.length >= marker.length * 2 &&
    selected.startsWith(marker) &&
    selected.endsWith(marker)
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
    if (before.endsWith(marker) && after.startsWith(marker)) {
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
