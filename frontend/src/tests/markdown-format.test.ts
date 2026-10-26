import { describe, expect, it } from 'vitest';
import {
  markerForShortcut,
  toggleMarkdown,
  type MarkdownEdit,
  type MarkdownMarker,
} from '../lib/markdown-format';

/**
 * The toolbar edits Markdown source through this function alone, so these cases pin the text and
 * the resulting selection for every shape the editor can meet: a selection wrapped from either
 * side, a collapsed caret, both ends of the field, and marker characters that already occur in the
 * content. A wrong offset here puts the caret in the middle of the user's word even though the
 * text looks right.
 */
describe('toggleMarkdown', () => {
  interface Case {
    name: string;
    text: string;
    start: number;
    end: number;
    marker: MarkdownMarker;
    expected: MarkdownEdit;
  }

  const cases: Case[] = [
    {
      name: 'wraps a selected word and keeps it selected inside the pair',
      text: 'Hello world',
      start: 0,
      end: 5,
      marker: '**',
      expected: { text: '**Hello** world', start: 2, end: 7 },
    },
    {
      name: 'wraps a selection in the middle of the text',
      text: 'abc',
      start: 1,
      end: 2,
      marker: '*',
      expected: { text: 'a*b*c', start: 2, end: 3 },
    },
    {
      name: 'wraps a whole field at both edges',
      text: 'Hello',
      start: 0,
      end: 5,
      marker: '*',
      expected: { text: '*Hello*', start: 1, end: 6 },
    },
    {
      name: 'removes a pair that sits outside the selection',
      text: '**Hello** world',
      start: 2,
      end: 7,
      marker: '**',
      expected: { text: 'Hello world', start: 0, end: 5 },
    },
    {
      name: 'removes a pair that sits inside the selection',
      text: '**Hello**',
      start: 0,
      end: 9,
      marker: '**',
      expected: { text: 'Hello', start: 0, end: 5 },
    },
    {
      name: 'removes strikethrough around the selection',
      text: 'a ~~gone~~ b',
      start: 4,
      end: 8,
      marker: '~~',
      expected: { text: 'a gone b', start: 2, end: 6 },
    },
    {
      name: 'inserts an empty pair and puts the caret between the markers',
      text: '',
      start: 0,
      end: 0,
      marker: '**',
      expected: { text: '****', start: 2, end: 2 },
    },
    {
      name: 'inserts the pair at a caret in the middle',
      text: 'ab',
      start: 1,
      end: 1,
      marker: '**',
      expected: { text: 'a****b', start: 3, end: 3 },
    },
    {
      name: 'a second press between the markers removes the pair',
      text: 'a****b',
      start: 3,
      end: 3,
      marker: '**',
      expected: { text: 'ab', start: 1, end: 1 },
    },
    {
      name: 'a caret between strikethrough markers removes them',
      text: 'x~~~~y',
      start: 3,
      end: 3,
      marker: '~~',
      expected: { text: 'xy', start: 1, end: 1 },
    },
    {
      name: 'keeps marker characters that are part of the selected text',
      text: 'a*b',
      start: 0,
      end: 3,
      marker: '*',
      expected: { text: '*a*b*', start: 1, end: 4 },
    },
    {
      name: 'unwraps a selection that contains marker characters',
      text: '**a**b**',
      start: 2,
      end: 6,
      marker: '**',
      expected: { text: 'a**b', start: 0, end: 4 },
    },
    {
      name: 'treats reversed offsets as a normal selection',
      text: 'Hello',
      start: 5,
      end: 0,
      marker: '**',
      expected: { text: '**Hello**', start: 2, end: 7 },
    },
    {
      name: 'clamps offsets that lie outside the text',
      text: 'Hi',
      start: -3,
      end: 99,
      marker: '**',
      expected: { text: '**Hi**', start: 2, end: 4 },
    },
    {
      name: 'does not unwrap a pair that covers only one side of the selection',
      text: '**Hello',
      start: 2,
      end: 7,
      marker: '**',
      expected: { text: '****Hello**', start: 4, end: 9 },
    },
    {
      name: 'adds italic around a fully selected bold run instead of replacing bold',
      text: '**bold**',
      start: 0,
      end: 8,
      marker: '*',
      expected: { text: '***bold***', start: 1, end: 9 },
    },
    {
      name: 'adds italic to the inner text of a bold run instead of stripping one star',
      text: '**bold**',
      start: 2,
      end: 6,
      marker: '*',
      expected: { text: '***bold***', start: 3, end: 7 },
    },
    {
      name: 'adds bold around italic text',
      text: '*italic*',
      start: 0,
      end: 8,
      marker: '**',
      expected: { text: '***italic***', start: 2, end: 10 },
    },
    {
      name: 'removes the bold pair from bold plus italic text and leaves italic',
      text: '***both***',
      start: 0,
      end: 10,
      marker: '**',
      expected: { text: '*both*', start: 0, end: 6 },
    },
    {
      name: 'removes strikethrough on a fully selected run',
      text: '~~x~~',
      start: 0,
      end: 5,
      marker: '~~',
      expected: { text: 'x', start: 0, end: 1 },
    },
    {
      name: 'does not treat one star of a bold pair as an italic pair at the caret',
      text: '****',
      start: 2,
      end: 2,
      marker: '*',
      expected: { text: '******', start: 3, end: 3 },
    },
  ];

  it.each(cases)('$name', ({ text, start, end, marker, expected }) => {
    expect(toggleMarkdown(text, start, end, marker)).toEqual(expected);
  });

  it('leaves the text unchanged when the marker already wraps the whole field and the whole field is selected', () => {
    const edit = toggleMarkdown('~~gone~~', 0, 8, '~~');
    expect(edit.text).toBe('gone');
    expect(edit.start).toBe(0);
    expect(edit.end).toBe(4);
  });
});

/**
 * The shortcut mapping is checked without a browser event so that a key bound on one platform is
 * never silently missing on another, and so that Shift/Alt combinations the editor does not own
 * stay with the browser.
 */
describe('markerForShortcut', () => {
  it.each([
    [{ key: 'b', ctrlKey: true }, '**'],
    [{ key: 'B', metaKey: true }, '**'],
    [{ key: 'i', ctrlKey: true }, '*'],
    [{ key: 'I', metaKey: true }, '*'],
    [{ key: 'x', ctrlKey: true, shiftKey: true }, '~~'],
    [{ key: 'X', metaKey: true, shiftKey: true }, '~~'],
  ] as const)('maps %j to %s', (input, expected) => {
    expect(markerForShortcut(input)).toBe(expected);
  });

  it.each([
    { key: 'b' },
    { key: 'b', ctrlKey: true, shiftKey: true },
    { key: 'i', ctrlKey: true, shiftKey: true },
    { key: 'x', ctrlKey: true },
    { key: 'b', ctrlKey: true, altKey: true },
    { key: 'u', ctrlKey: true },
  ])('ignores %j', (input) => {
    expect(markerForShortcut(input)).toBeNull();
  });
});
