import { describe, it, expect } from 'vitest';
import {
  parseClozes,
  clozedText,
  extractPhysicalBlanks,
  groupPhysicalBlanks,
  parseClozeMetadata,
  computeUnlinkedAlternatives,
  serializeClozeMetadata,
  reassociateUnlinked,
  deleteUnlinked,
  normalizeAnswer,
  htmlToPlain,
  parseClozeOpen,
  isEntryInvalid,
  repairInvalidEntry,
} from '../lib/cloze-alternatives';

describe('cloze parsing and blank extraction', () => {
  it('parses ordinary cloze deletions with hints', () => {
    const text = '{{c1::Paris::capital}} is in {{c2::France}}.';
    const blanks = extractPhysicalBlanks(text);
    expect(blanks).toHaveLength(2);
    expect(blanks[0]).toMatchObject({
      clozeOrdinal: 1,
      canonical: 'Paris',
      hint: 'capital',
    });
    expect(blanks[1]).toMatchObject({
      clozeOrdinal: 2,
      canonical: 'France',
      hint: undefined,
    });
  });

  it('handles multi-number deletions like {{c1,2::Europe}}', () => {
    const text = '{{c1,2::Europe}} is a continent.';
    const blanks = extractPhysicalBlanks(text);
    expect(blanks).toHaveLength(2);
    expect(blanks[0]!.clozeOrdinal).toBe(1);
    expect(blanks[0]!.canonical).toBe('Europe');
    expect(blanks[1]!.clozeOrdinal).toBe(2);
    expect(blanks[1]!.canonical).toBe('Europe');
  });

  it('handles nested clozes without failing or dropping inner content', () => {
    const text = '{{c1::outer {{c2::inner}}}}';
    const nodes = parseClozes(text);
    expect(nodes).toHaveLength(1);
    expect(nodes[0]!.cloze).toBeDefined();
    expect(clozedText(nodes[0]!.cloze!)).toBe('outer inner');

    const blanks = extractPhysicalBlanks(text);
    expect(blanks).toHaveLength(2);
    expect(blanks[0]!).toMatchObject({ clozeOrdinal: 1, canonical: 'outer inner' });
    expect(blanks[1]!).toMatchObject({ clozeOrdinal: 2, canonical: 'inner' });
  });

  it('groups multiple blanks with same cloze number and exact canonical text', () => {
    const text = '{{c1::is}} Paris, and {{c1::is}} Rome.';
    const blanks = extractPhysicalBlanks(text);
    expect(blanks).toHaveLength(2);

    const groups = groupPhysicalBlanks(blanks);
    expect(groups).toHaveLength(1);
    expect(groups[0]!.clozeOrdinal).toBe(1);
    expect(groups[0]!.canonical).toBe('is');
    expect(groups[0]!.occurrences).toHaveLength(2);
  });
});

describe('cloze metadata parsing and preservation', () => {
  it('preserves unknown root keys when saving', () => {
    const raw = JSON.stringify({
      version: 2,
      custom_flag: true,
      cloze_accept: {
        '1': {
          Paris: ['City of Light'],
        },
      },
    });

    const parsed = parseClozeMetadata(raw);
    expect(parsed.valid).toBe(true);
    expect(parsed.unknownRootKeys).toEqual({ version: 2, custom_flag: true });
    expect(parsed.clozeAccept['1']!['Paris']).toEqual(['City of Light']);

    // Add another alternative
    parsed.clozeAccept['1']!['Paris']!.push('Lutetia');
    const serialized = JSON.parse(serializeClozeMetadata(parsed));
    expect(serialized.version).toBe(2);
    expect(serialized.custom_flag).toBe(true);
    expect(serialized.cloze_accept['1']['Paris']).toEqual(['City of Light', 'Lutetia']);
  });

  it('detects malformed JSON or non-object content and preserves it unchanged', () => {
    const badJson = '<p>not valid json</p>';
    const parsed = parseClozeMetadata(badJson);
    expect(parsed.valid).toBe(false);
    expect(parsed.raw).toBe(badJson);

    // Serialization of invalid metadata must return the original raw string verbatim
    const serialized = serializeClozeMetadata(parsed);
    expect(serialized).toBe(badJson);

    const arrayJson = '[1, 2, 3]';
    const parsedArray = parseClozeMetadata(arrayJson);
    expect(parsedArray.valid).toBe(false);
    expect(serializeClozeMetadata(parsedArray)).toBe(arrayJson);
  });

  it('preserves invalid subtrees instead of silently dropping them', () => {
    const raw = JSON.stringify({
      cloze_accept: {
        '1': {
          Paris: ['City of Light'],
          MalformedEntry: 12345, // Not an array
        },
        '2': 'not_an_object_group', // Malformed group
      },
    });

    const parsed = parseClozeMetadata(raw);
    expect(parsed.valid).toBe(true);
    expect(parsed.invalidGroups['2']).toBe('not_an_object_group');
    expect(parsed.invalidEntries['1']!['MalformedEntry']).toBe(12345);
    expect(parsed.clozeAccept['1']!['Paris']).toEqual(['City of Light']);

    const serialized = JSON.parse(serializeClozeMetadata(parsed));
    expect(serialized.cloze_accept['1']['Paris']).toEqual(['City of Light']);
    expect(serialized.cloze_accept['1']['MalformedEntry']).toBe(12345);
    expect(serialized.cloze_accept['2']).toBe('not_an_object_group');
  });

  it('detects unlinked alternatives and supports explicit reassociation and deletion', () => {
    const activeText = '{{c1::Paris}} is the capital.';
    const blanks = extractPhysicalBlanks(activeText);
    const groups = groupPhysicalBlanks(blanks);

    // Metadata has c1 Paris AND an old unlinked c1 London
    const raw = JSON.stringify({
      cloze_accept: {
        '1': {
          Paris: ['City of Light'],
          London: ['Capital of UK'],
        },
      },
    });

    const parsed = parseClozeMetadata(raw);
    const unlinked = computeUnlinkedAlternatives(parsed.clozeAccept, groups);
    expect(unlinked).toHaveLength(1);
    expect(unlinked[0]).toEqual({
      clozeNumber: '1',
      canonical: 'London',
      alternatives: ['Capital of UK'],
      isInvalid: false,
    });

    // Reassociate London's alternative to Paris
    reassociateUnlinked(parsed, unlinked[0]!, groups[0]!);
    const unlinkedAfter = computeUnlinkedAlternatives(parsed.clozeAccept, groups);
    expect(unlinkedAfter).toHaveLength(0);
    expect(parsed.clozeAccept['1']!['Paris']).toEqual(['City of Light', 'Capital of UK']);
    expect(parsed.clozeAccept['1']?.['London']).toBeUndefined();

    // Now test delete unlinked
    const meta2 = parseClozeMetadata(raw);
    const unlinked2 = computeUnlinkedAlternatives(meta2.clozeAccept, groups);
    deleteUnlinked(meta2, unlinked2[0]!);
    const unlinked2After = computeUnlinkedAlternatives(meta2.clozeAccept, groups);
    expect(unlinked2After).toHaveLength(0);
    expect(meta2.clozeAccept['1']?.['London']).toBeUndefined();
    expect(meta2.clozeAccept['1']!['Paris']).toEqual(['City of Light']);
  });

  it('faithfully converts HTML to plain text without collapsing internal spaces or breaking formatting joins', () => {
    // <b>is</b>are should join as isare, without inserted spaces
    expect(htmlToPlain('<b>is</b>are')).toBe('isare');
    // <br> or <div> should become space
    expect(htmlToPlain('one<br>two<div>three</div>')).toBe('one two three');
    // Entities decoded
    expect(htmlToPlain('&lt;hello&gt;&nbsp;&amp;&nbsp;&quot;world&quot;')).toBe('<hello> & "world"');
  });

  it('matches Go uint16 token boundaries while excluding zero from blanks', () => {
    expect(parseClozeOpen('{{c0::foo}}')?.ordinals).toEqual([0]);
    expect(parseClozeOpen('{{c65536::foo}}')).toBeNull();
    expect(parseClozeOpen('{{c,0001,,65536,0::foo}}')?.ordinals).toEqual([0, 1]);
    expect(parseClozeOpen('{{c1x::foo}}')).toBeNull();
    expect(extractPhysicalBlanks('{{c0::outer {{c1::inner}}}}').map(b => b.canonical)).toEqual(['inner']);
    const valid = parseClozeOpen('{{c1,65535::foo}}');
    expect(valid).not.toBeNull();
    expect(valid!.ordinals).toEqual([1, 65535]);
  });

  it('preserves distinct accurate context excerpts for duplicate identical blanks', () => {
    const text = 'First occurrence {{c1::is}} here, and second occurrence {{c1::is}} there.';
    const blanks = extractPhysicalBlanks(text);
    expect(blanks).toHaveLength(2);
    expect(blanks[0]!.contextBefore).toContain('First occurrence');
    expect(blanks[0]!.contextAfter).toContain('here');
    expect(blanks[1]!.contextBefore).toContain('second occurrence');
    expect(blanks[1]!.contextAfter).toContain('there');
  });

  it('normalizes answers matching backend comparison', () => {
    expect(normalizeAnswer('  Hello   World  ')).toBe('hello world');
    expect(normalizeAnswer('Paris')).toBe('paris');
  });

  it('safely roundtrips canonical "__proto__", "constructor", and unknown nested "__proto__" without prototype pollution', () => {
    // In JS object literals, use computed property ['__proto__'] so it creates an own property rather than setting object prototype
    const raw = JSON.stringify({
      ['__proto__']: { danger: 'attack' },
      ['constructor']: 'ctor_value',
      cloze_accept: {
        '1': {
          ['__proto__']: ['alt_proto'],
          ['constructor']: ['alt_ctor'],
        },
      },
    });

    const parsed = parseClozeMetadata(raw);
    expect(parsed.valid).toBe(true);

    // Verify Object.prototype was NOT polluted
    expect((Object.prototype as any).danger).toBeUndefined();
    expect((Object.prototype as any).alt_proto).toBeUndefined();

    // Verify parsed dictionary accurately kept values
    expect(parsed.unknownRootKeys['__proto__']).toEqual({ danger: 'attack' });
    expect(parsed.unknownRootKeys['constructor']).toBe('ctor_value');
    expect(parsed.clozeAccept['1']!['__proto__']).toEqual(['alt_proto']);
    expect(parsed.clozeAccept['1']!['constructor']).toEqual(['alt_ctor']);

    // Serialized output must roundtrip all keys including __proto__ and constructor
    const serialized = serializeClozeMetadata(parsed);
    expect(JSON.parse(serialized)['__proto__']).toEqual({ danger: 'attack' });
    expect(JSON.parse(serialized).cloze_accept['1']['__proto__']).toEqual(['alt_proto']);
  });

  it('preserves invalid capability (false, []) without silently converting to empty object', () => {
    const rawBool = JSON.stringify({ cloze_accept: false, other: 123 });
    const parsedBool = parseClozeMetadata(rawBool);
    expect(parsedBool.valid).toBe(false);
    expect(JSON.parse(serializeClozeMetadata(parsedBool))).toEqual(JSON.parse(rawBool));

    const rawArr = JSON.stringify({ cloze_accept: [], flag: true });
    const parsedArr = parseClozeMetadata(rawArr);
    expect(parsedArr.valid).toBe(false);
    expect(JSON.parse(serializeClozeMetadata(parsedArr))).toEqual(JSON.parse(rawArr));
  });

  it('preserves invalid mixed arrays on un-repaired entries while allowing siblings to be edited', () => {
    const raw = JSON.stringify({
      cloze_accept: {
        '1': {
          Paris: ['City of Light', 12345], // Mixed array (invalid)
          Rome: ['Eternal City'],          // Valid sibling
        },
      },
    });

    const parsed = parseClozeMetadata(raw);
    expect(parsed.valid).toBe(true);
    expect(isEntryInvalid(parsed, '1', 'Paris')).toBe(true);
    expect(isEntryInvalid(parsed, '1', 'Rome')).toBe(false);

    // Edit the valid sibling Rome
    parsed.clozeAccept['1']!['Rome']!.push('Roma');

    // Without repairing Paris, serialization must preserve Paris's mixed array [City of Light, 12345] untouched!
    const serialized = JSON.parse(serializeClozeMetadata(parsed));
    expect(serialized.cloze_accept['1']['Paris']).toEqual(['City of Light', 12345]);
    expect(serialized.cloze_accept['1']['Rome']).toEqual(['Eternal City', 'Roma']);

    // Explicitly repair Paris
    repairInvalidEntry(parsed, '1', 'Paris');
    expect(isEntryInvalid(parsed, '1', 'Paris')).toBe(false);

    // After repair, serialization emits the clean string array
    const serializedRepaired = JSON.parse(serializeClozeMetadata(parsed));
    expect(serializedRepaired.cloze_accept['1']['Paris']).toEqual(['City of Light']);
  });
});
