import { describe, expect, it } from 'vitest';
import {
  insertCodeBlock,
  insertLink,
  insertTable,
  markerForShortcut,
  toggleLinePrefix,
  toggleMarkdown,
  type LinePrefix,
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
    {
      name: 'wraps a selection in a code span',
      text: 'run ls now',
      start: 4,
      end: 6,
      marker: '`',
      expected: { text: 'run `ls` now', start: 5, end: 7 },
    },
    {
      name: 'removes a code span around the selection',
      text: 'run `ls` now',
      start: 5,
      end: 7,
      marker: '`',
      expected: { text: 'run ls now', start: 4, end: 6 },
    },
    {
      name: 'does not read a code fence as a code span pair',
      text: '```x```',
      start: 3,
      end: 4,
      marker: '`',
      expected: { text: '````x````', start: 4, end: 5 },
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

/**
 * Line blocks rewrite whole lines, so these cases pin which lines a selection touches, how an
 * existing marker of the same or the other list kind is treated, and where the caret ends up.
 */
describe('toggleLinePrefix', () => {
  interface Case {
    name: string;
    text: string;
    start: number;
    end: number;
    kind: LinePrefix;
    expected: MarkdownEdit;
  }

  const cases: Case[] = [
    {
      name: 'quotes the caret line and keeps the caret on the same character',
      text: 'one\ntwo',
      start: 5,
      end: 5,
      kind: 'quote',
      expected: { text: 'one\n> two', start: 7, end: 7 },
    },
    {
      name: 'unquotes a quoted caret line',
      text: '> two',
      start: 3,
      end: 3,
      kind: 'quote',
      expected: { text: 'two', start: 1, end: 1 },
    },
    {
      name: 'keeps the caret at the line start when the removed prefix was in front of it',
      text: '> two',
      start: 1,
      end: 1,
      kind: 'quote',
      expected: { text: 'two', start: 0, end: 0 },
    },
    {
      name: 'bullets every selected line and selects the edited block',
      text: 'a\nb',
      start: 0,
      end: 3,
      kind: 'bullet',
      expected: { text: '- a\n- b', start: 0, end: 7 },
    },
    {
      name: 'numbers non-empty lines from one and leaves a blank line empty',
      text: 'a\n\nb',
      start: 0,
      end: 4,
      kind: 'ordered',
      expected: { text: '1. a\n\n2. b', start: 0, end: 10 },
    },
    {
      name: 'removes bullets when every selected line already has one',
      text: '- a\n- b',
      start: 0,
      end: 7,
      kind: 'bullet',
      expected: { text: 'a\nb', start: 0, end: 3 },
    },
    {
      name: 'adds the prefix to all lines when only some carry it',
      text: '> a\nb',
      start: 0,
      end: 5,
      kind: 'quote',
      expected: { text: '> a\n> b', start: 0, end: 7 },
    },
    {
      name: 'converts a bulleted list into a numbered one instead of nesting the markers',
      text: '- a\n- b',
      start: 0,
      end: 7,
      kind: 'ordered',
      expected: { text: '1. a\n2. b', start: 0, end: 9 },
    },
    {
      name: 'excludes the line a selection ends at the very start of',
      text: 'a\nb',
      start: 0,
      end: 2,
      kind: 'quote',
      expected: { text: '> a\nb', start: 0, end: 3 },
    },
    {
      name: 'starts an item on an empty field',
      text: '',
      start: 0,
      end: 0,
      kind: 'bullet',
      expected: { text: '- ', start: 2, end: 2 },
    },
  ];

  it.each(cases)('$name', ({ text, start, end, kind, expected }) => {
    expect(toggleLinePrefix(text, start, end, kind)).toEqual(expected);
  });
});

/** A fenced block always stands on lines of its own, so the cases pin the padding at each position. */
describe('insertCodeBlock', () => {
  it.each([
    {
      name: 'inserts an empty block on an empty field with the caret inside',
      text: '',
      start: 0,
      end: 0,
      expected: { text: '```\n\n```', start: 4, end: 4 },
    },
    {
      name: 'wraps a selection and keeps it selected',
      text: 'x = 1',
      start: 0,
      end: 5,
      expected: { text: '```\nx = 1\n```', start: 4, end: 9 },
    },
    {
      name: 'pads a block inserted after a paragraph with a blank line',
      text: 'Intro',
      start: 5,
      end: 5,
      expected: { text: 'Intro\n\n```\n\n```', start: 11, end: 11 },
    },
    {
      name: 'pads off text on both sides of a selection in the middle of a line',
      text: 'a b c',
      start: 2,
      end: 3,
      expected: { text: 'a \n\n```\nb\n```\n\n c', start: 8, end: 9 },
    },
    {
      name: 'keeps a trailing newline of the selection outside the closing fence',
      text: 'x\nnext',
      start: 0,
      end: 2,
      expected: { text: '```\nx\n```\n\nnext', start: 4, end: 5 },
    },
  ])('$name', ({ text, start, end, expected }) => {
    expect(insertCodeBlock(text, start, end)).toEqual(expected);
  });
});

/** A table never replaces the writer's text, and the first header cell is ready to be typed over. */
describe('insertTable', () => {
  const headers = ['H1', 'H2'] as const;

  it.each([
    {
      name: 'inserts a table on an empty field and selects the first header',
      text: '',
      start: 0,
      end: 0,
      expected: { text: '| H1 | H2 |\n| --- | --- |\n|  |  |', start: 2, end: 4 },
    },
    {
      name: 'puts the table after a selection without removing it',
      text: 'keep me',
      start: 0,
      end: 4,
      expected: { text: 'keep\n\n| H1 | H2 |\n| --- | --- |\n|  |  |\n\n me', start: 8, end: 10 },
    },
    {
      name: 'reuses the newline that already ends the previous line',
      text: 'Intro\n',
      start: 6,
      end: 6,
      expected: { text: 'Intro\n\n| H1 | H2 |\n| --- | --- |\n|  |  |', start: 9, end: 11 },
    },
  ])('$name', ({ text, start, end, expected }) => {
    expect(insertTable(text, start, end, headers)).toEqual(expected);
  });
});

describe('insertLink', () => {
  it.each([
    {
      name: 'inserts an empty link at the caret and waits in its text',
      text: 'see ',
      start: 4,
      end: 4,
      expected: { text: 'see [](https://)', start: 5, end: 5 },
    },
    {
      name: 'uses the selection as the link text and selects the placeholder target',
      text: 'see docs',
      start: 4,
      end: 8,
      expected: { text: 'see [docs](https://)', start: 11, end: 19 },
    },
    {
      name: 'uses a selected web address as the target and waits in the empty text',
      text: 'https://example.com',
      start: 0,
      end: 19,
      expected: { text: '[](https://example.com)', start: 1, end: 1 },
    },
  ])('$name', ({ text, start, end, expected }) => {
    expect(insertLink(text, start, end)).toEqual(expected);
  });
});
