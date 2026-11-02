// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
//
// Ported from rslib/src/cloze.rs at tag 26.09.3 of ankitects/anki and
// internal/notetype/cloze.go & plaintext.go of engram.

export interface ClozeToken {
  open?: boolean;
  ordinals?: number[];
  text?: string;
  closing?: boolean;
  start: number;
  end: number;
}

export interface ClozeNode {
  text?: string;
  cloze?: ClozeDeletion;
}

export interface ClozeDeletion {
  ordinals: number[];
  children: ClozeNode[];
  hint?: string;
  hasHint: boolean;
  startIndex: number;
  endIndex: number;
}

const clozeOpenMarker = '{{c';
const clozeCloseMarker = '}}';
const clozeImagePrefix = 'image-occlusion:';
const clozeMaxNesting = 10;

/**
 * Creates a prototype-less dictionary (Object.create(null)).
 * Protects against prototype pollution and key loss for special properties like "__proto__", "constructor", "toString".
 */
export function createDict<T>(): Record<string, T> {
  return Object.create(null) as Record<string, T>;
}

/**
 * Recognises "{{c<numbers>::" at the start of s.
 * The tokenizer accepts uint16 ordinals, including the image-occlusion placeholder 0.
 */
export function parseClozeOpen(s: string): { ordinals: number[]; matchLength: number } | null {
  if (!s.startsWith(clozeOpenMarker)) return null;
  let i = clozeOpenMarker.length;
  const start = i;
  while (i < s.length) {
    const char = s[i];
    if (char && ((char >= '0' && char <= '9') || char === ',')) {
      i++;
    } else {
      break;
    }
  }
  const numbersStr = s.slice(start, i);
  const rawOrdinals: number[] = [];
  for (const part of numbersStr.split(',')) {
    const val = Number(part);
    if (part !== '' && Number.isInteger(val) && val <= 65535) {
      rawOrdinals.push(val);
    }
  }
  const ordinals = Array.from(new Set(rawOrdinals)).sort((a, b) => a - b);
  if (ordinals.length === 0) return null;
  if (!s.slice(i).startsWith('::')) return null;
  return { ordinals, matchLength: i + 2 };
}

/**
 * Splits text into cloze markers and literal text, tracking exact start and end offsets.
 */
export function clozeTokens(text: string): ClozeToken[] {
  const tokens: ClozeToken[] = [];
  let offset = 0;
  while (offset < text.length) {
    const slice = text.slice(offset);
    const openMatch = parseClozeOpen(slice);
    if (openMatch) {
      tokens.push({
        open: true,
        ordinals: openMatch.ordinals,
        start: offset,
        end: offset + openMatch.matchLength,
      });
      offset += openMatch.matchLength;
      continue;
    }
    if (slice.startsWith(clozeCloseMarker)) {
      tokens.push({
        closing: true,
        start: offset,
        end: offset + clozeCloseMarker.length,
      });
      offset += clozeCloseMarker.length;
      continue;
    }
    let nextIndex = slice.length;
    for (let i = 1; i < slice.length; i++) {
      if (slice.slice(i).startsWith(clozeCloseMarker) || parseClozeOpen(slice.slice(i))) {
        nextIndex = i;
        break;
      }
    }
    tokens.push({
      text: slice.slice(0, nextIndex),
      start: offset,
      end: offset + nextIndex,
    });
    offset += nextIndex;
  }
  return tokens;
}

/**
 * Builds the AST node tree from cloze tokens, recording exact start and end offsets of deletions.
 */
export function parseClozes(text: string): ClozeNode[] {
  const open: ClozeDeletion[] = [];
  const output: ClozeNode[] = [];
  for (const token of clozeTokens(text)) {
    if (token.open && token.ordinals) {
      if (open.length < clozeMaxNesting) {
        open.push({
          ordinals: token.ordinals,
          children: [],
          hasHint: false,
          startIndex: token.start,
          endIndex: token.end,
        });
      }
    } else if (token.closing) {
      if (open.length === 0) {
        output.push({ text: clozeCloseMarker });
        continue;
      }
      const deletion = open.pop()!;
      deletion.endIndex = token.end;
      if (open.length === 0) {
        output.push({ cloze: deletion });
      } else {
        open.at(-1)!.children.push({ cloze: deletion });
      }
    } else if (token.text !== undefined) {
      let literal = token.text;
      if (open.length > 0) {
        const deletion = open.at(-1)!;
        if (!literal.startsWith(clozeImagePrefix)) {
          const colonIdx = literal.indexOf('::');
          if (colonIdx !== -1) {
            deletion.hint = literal.slice(colonIdx + 2);
            deletion.hasHint = true;
            literal = literal.slice(0, colonIdx);
          }
        }
        deletion.children.push({ text: literal });
      } else {
        output.push({ text: literal });
      }
    }
  }
  return output;
}

/**
 * Returns the inner text of a cloze deletion without the marker.
 */
export function clozedText(deletion: ClozeDeletion): string {
  if (deletion.children.length === 1 && !deletion.children[0]?.cloze) {
    return deletion.children[0]?.text ?? '';
  }
  let out = '';
  for (const child of deletion.children) {
    if (child.cloze) {
      out += clozedText(child.cloze);
    } else {
      out += child.text ?? '';
    }
  }
  return out;
}

export interface PhysicalBlank {
  clozeOrdinal: number;
  canonical: string;
  hint?: string;
  contextBefore: string;
  contextAfter: string;
  occurrenceIndex: number;
  startIndex: number;
  endIndex: number;
}

// Spacer tags that Anki / Engram maps to a single space (internal/notetype/plaintext.go)
const plainTextSpacers = /<br\s*\/?>|<div>|\n/gi;

// Tag regex matching PlainText and stripAVTags:
// 1. [anki:tts ...]...[/anki:tts] directive is audio only; body is spoken, not shown (internal/notetype/av.go stripAVTags).
const ttsDirectivePattern = /\[anki:tts(?:\s+[^\]]*)?\][\s\S]*?\[\/anki:tts\]/g;

// 2. Sound tags contribute no text: [sound:filename] (internal/notetype/plaintext.go plainTextSoundTag).
const soundTagPattern = /\[sound:[^\]]+\]/g;

// 3. Type-in placeholder [[type:...]] is not text (internal/notetype/plaintext.go plainTextTypeTag).
const typeTagPattern = /\[\[type:[^\]]+\]\]/g;

// Markup tags, comments, script, and style removed without inserting a space (internal/notetype/filters.go htmlPattern).
const htmlPattern = /(<!--[\s\S]*?-->)|(<style\b[\s\S]*?<\/style>)|(<script\b[\s\S]*?<\/script>)|(<[\s\S]*?>)/gi;

const fallbackEntityMap: Record<string, string> = {
  nbsp: ' ',
  amp: '&',
  lt: '<',
  gt: '>',
  quot: '"',
  apos: "'",
};

/**
 * Decodes HTML entities matching Go decodeEntities:
 * Decodes standard named and numeric entities in a single pass; non-breaking space becomes an ordinary space.
 */
function decodeEntities(text: string): string {
  if (!text.includes('&')) return text;
  if (typeof document !== 'undefined') {
    const el = document.createElement('textarea');
    el.innerHTML = text;
    return el.value.replace(/\u00a0/g, ' ');
  }
  return text
    .replace(/&(#x[0-9a-f]+|#[0-9]+|[a-z0-9]+);/gi, (match, ent) => {
      const lower = ent.toLowerCase();
      if (fallbackEntityMap[lower] !== undefined) return fallbackEntityMap[lower];
      if (lower.startsWith('#x')) {
        return String.fromCodePoint(parseInt(lower.slice(2), 16));
      }
      if (lower.startsWith('#')) {
        return String.fromCodePoint(parseInt(lower.slice(1), 10));
      }
      return match;
    })
    .replace(/\u00a0/g, ' ');
}

/**
 * Converts HTML to plain text faithfully matching Go notetype.PlainText:
 * 1. Strips entire TTS directives and sound tags (stripAVTags);
 * 2. Maps <br>, <div>, and literal \n to a single space (plainTextSpacers);
 * 3. Drops type-in placeholders [[type:...]] and sound tags (plainTextTypeTag, plainTextSoundTag);
 * 4. Strips all other HTML markup, comments, script, and style without inserting spaces (filters.go htmlPattern);
 * 5. Decodes HTML entities in a single pass (decodeEntities) and trims outer ends.
 * Preserves formatting-less joins (e.g. <b>is</b>are -> isare) and internal spacing.
 */
export function htmlToPlain(html: string): string {
  // Strip AV tags: entire TTS directives and sound tags (internal/notetype/av.go stripAVTags)
  let text = html.replace(ttsDirectivePattern, '');
  text = text.replace(soundTagPattern, '');
  // Maps line breaks, opening <div>, and literal \n to a single space (internal/notetype/plaintext.go)
  text = text.replace(plainTextSpacers, ' ');
  // Type-in placeholders and sound tags contribute no text
  text = text.replace(typeTagPattern, '');
  text = text.replace(soundTagPattern, '');
  // Strip HTML comments, style, script, and tags without space (internal/notetype/filters.go)
  const stripped = text.replace(htmlPattern, '');
  // Decode HTML entities (single pass) and trim outer ends
  return decodeEntities(stripped).trim();
}

/**
 * Extracts all physical cloze blanks from the note's bound text field, in order of appearance.
 * Uses exact deletion character offsets for context extraction so duplicate identical wordings
 * each receive their own correct surrounding prose snippet.
 */
export function extractPhysicalBlanks(fieldContent: string): PhysicalBlank[] {
  const plain = htmlToPlain(fieldContent);
  const nodes = parseClozes(plain);
  const blanks: PhysicalBlank[] = [];
  let blankCount = 0;

  function collectFromNode(node: ClozeNode): void {
    if (!node.cloze) return;
    const deletion = node.cloze;
    const canonical = clozedText(deletion);
    const hint = deletion.hasHint ? deletion.hint : undefined;

    // For every ordinal this deletion addresses (usually one, e.g. [1])
    for (const ord of deletion.ordinals) {
      if (ord === 0) continue;
      blankCount++;

      // Use deletion's real start and end indices in plain text
      const start = Math.max(0, deletion.startIndex - 20);
      const contextBefore = (start > 0 ? '…' : '') + plain.slice(start, deletion.startIndex);
      const end = Math.min(plain.length, deletion.endIndex + 20);
      const contextAfter = plain.slice(deletion.endIndex, end) + (end < plain.length ? '…' : '');

      blanks.push({
        clozeOrdinal: ord,
        canonical,
        hint,
        contextBefore,
        contextAfter,
        occurrenceIndex: blankCount,
        startIndex: deletion.startIndex,
        endIndex: deletion.endIndex,
      });
    }

    for (const child of deletion.children) {
      collectFromNode(child);
    }
  }

  for (const node of nodes) {
    collectFromNode(node);
  }

  return blanks;
}

export interface BlankGroup {
  key: string;
  clozeOrdinal: number;
  canonical: string;
  occurrences: PhysicalBlank[];
}

/**
 * Groups physical blanks by (clozeOrdinal, canonical) so identical blanks under the same cloze number share one group.
 */
export function groupPhysicalBlanks(blanks: PhysicalBlank[]): BlankGroup[] {
  const groups: BlankGroup[] = [];
  const map = new Map<string, BlankGroup>();

  for (const blank of blanks) {
    const key = `${blank.clozeOrdinal}:::${blank.canonical}`;
    let existing = map.get(key);
    if (!existing) {
      existing = {
        key,
        clozeOrdinal: blank.clozeOrdinal,
        canonical: blank.canonical,
        occurrences: [],
      };
      map.set(key, existing);
      groups.push(existing);
    }
    existing.occurrences.push(blank);
  }

  return groups;
}

export interface ParsedClozeMetadata {
  valid: boolean;
  raw: string;
  unknownRootKeys: Record<string, unknown>;
  invalidCapability?: unknown;
  invalidGroups: Record<string, unknown>;
  invalidEntries: Record<string, Record<string, unknown>>;
  /** clozeAccept[numberStr][canonicalStr] = string[] */
  clozeAccept: Record<string, Record<string, string[]>>;
}

function setProp<T>(obj: Record<string, T>, key: string, value: T): void {
  Object.defineProperty(obj, key, {
    value,
    writable: true,
    enumerable: true,
    configurable: true,
  });
}

/**
 * Parses the raw engram_meta JSON string using null-prototype dictionaries.
 * Preserves unknown root keys, invalid capabilities (e.g. false, []), invalid subtrees,
 * and tracks mixed arrays without silent modifications.
 */
export function parseClozeMetadata(raw: string): ParsedClozeMetadata {
  const trimmed = raw.trim();
  if (trimmed === '') {
    return {
      valid: true,
      raw,
      unknownRootKeys: createDict(),
      invalidGroups: createDict(),
      invalidEntries: createDict(),
      clozeAccept: createDict(),
    };
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return {
      valid: false,
      raw,
      unknownRootKeys: createDict(),
      invalidGroups: createDict(),
      invalidEntries: createDict(),
      clozeAccept: createDict(),
    };
  }

  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return {
      valid: false,
      raw,
      unknownRootKeys: createDict(),
      invalidGroups: createDict(),
      invalidEntries: createDict(),
      clozeAccept: createDict(),
    };
  }

  const root = parsed as Record<string, unknown>;
  const unknownRootKeys = createDict<unknown>();
  for (const key of Reflect.ownKeys(root)) {
    if (typeof key === 'string' && key !== 'cloze_accept') {
      setProp(unknownRootKeys, key, root[key]);
    }
  }

  const invalidGroups = createDict<unknown>();
  const invalidEntries = createDict<Record<string, unknown>>();
  const clozeAccept = createDict<Record<string, string[]>>();

  if ('cloze_accept' in root) {
    const rawAccept = root['cloze_accept'];
    if (typeof rawAccept !== 'object' || rawAccept === null || Array.isArray(rawAccept)) {
      // Invalid capability: false, [], "string", number
      return {
        valid: false,
        raw,
        unknownRootKeys,
        invalidCapability: rawAccept,
        invalidGroups,
        invalidEntries,
        clozeAccept,
      };
    }

    for (const numKey of Reflect.ownKeys(rawAccept as Record<string, unknown>)) {
      if (typeof numKey !== 'string') continue;
      const groupVal = (rawAccept as Record<string, unknown>)[numKey];
      if (typeof groupVal !== 'object' || groupVal === null || Array.isArray(groupVal)) {
        setProp(invalidGroups, numKey, groupVal);
        continue;
      }

      const groupDict = createDict<string[]>();
      setProp(clozeAccept, numKey, groupDict);

      for (const canonKey of Reflect.ownKeys(groupVal as Record<string, unknown>)) {
        if (typeof canonKey !== 'string') continue;
        const entriesVal = (groupVal as Record<string, unknown>)[canonKey];
        if (!Array.isArray(entriesVal)) {
          let invForNum = invalidEntries[numKey];
          if (!invForNum) {
            invForNum = createDict<unknown>();
            setProp(invalidEntries, numKey, invForNum);
          }
          setProp(invForNum, canonKey, entriesVal);
          continue;
        }

        const stringItems: string[] = [];
        let hasNonString = false;
        for (const item of entriesVal) {
          if (typeof item === 'string') {
            stringItems.push(item);
          } else {
            hasNonString = true;
          }
        }

        setProp(groupDict, canonKey, stringItems);

        if (hasNonString) {
          // Mixed array: backend disables alternatives for this entry.
          // Store raw entriesVal in invalidEntries to preserve it untouched until explicitly repaired.
          let invForNum = invalidEntries[numKey];
          if (!invForNum) {
            invForNum = createDict<unknown>();
            setProp(invalidEntries, numKey, invForNum);
          }
          setProp(invForNum, canonKey, entriesVal);
        }
      }
    }
  }

  return {
    valid: true,
    raw,
    unknownRootKeys,
    invalidGroups,
    invalidEntries,
    clozeAccept,
  };
}

/**
 * Reports whether an entire cloze group (e.g. cloze_accept["1"]) is invalid (non-object, array, etc.).
 */
export function isGroupInvalid(meta: ParsedClozeMetadata | null | undefined, numKey: string): boolean {
  if (!meta || !meta.invalidGroups) return false;
  return meta.invalidGroups[numKey] !== undefined;
}

/**
 * Explicitly repairs an invalid group, clearing it from invalidGroups and initializing a clean dictionary in clozeAccept.
 */
export function repairInvalidGroup(meta: ParsedClozeMetadata, numKey: string): void {
  if (meta.invalidGroups) {
    delete meta.invalidGroups[numKey];
  }
  if (!meta.clozeAccept[numKey]) {
    setProp(meta.clozeAccept, numKey, createDict<string[]>());
  }
}

/**
 * Reports whether a wording entry currently holds an un-repaired invalid value (e.g. mixed array or non-array).
 */
export function isEntryInvalid(meta: ParsedClozeMetadata | null | undefined, numKey: string, canonKey: string): boolean {
  if (!meta || !meta.invalidEntries) return false;
  return meta.invalidEntries[numKey]?.[canonKey] !== undefined;
}

/**
 * Explicitly repairs an invalid entry, clearing its raw unparseable value and adopting the clean string array.
 */
export function repairInvalidEntry(meta: ParsedClozeMetadata, numKey: string, canonKey: string): void {
  if (!meta.clozeAccept[numKey]) setProp(meta.clozeAccept, numKey, createDict<string[]>());
  const cleanGroup = meta.clozeAccept[numKey]!;
  if (!Object.hasOwn(cleanGroup, canonKey)) setProp(cleanGroup, canonKey, []);
  const group = meta.invalidEntries[numKey];
  if (group) {
    delete group[canonKey];
    if (Reflect.ownKeys(group).length === 0) {
      delete meta.invalidEntries[numKey];
    }
  }
}

export interface UnlinkedItem {
  clozeNumber: string;
  canonical: string;
  alternatives: string[];
  isInvalid?: boolean;
}

/**
 * Computes unlinked alternatives reactively: any entry in clozeAccept or invalidEntries that does not match any active BlankGroup.
 */
export function computeUnlinkedAlternatives(
  metaOrAccept: ParsedClozeMetadata | Record<string, Record<string, string[]>>,
  activeGroups: BlankGroup[]
): UnlinkedItem[] {
  const meta = typeof metaOrAccept.valid === 'boolean' && typeof metaOrAccept.raw === 'string'
    ? metaOrAccept as ParsedClozeMetadata
    : undefined;
  const clozeAccept = meta?.clozeAccept
    ?? metaOrAccept as Record<string, Record<string, string[]>>;
  const unlinked: UnlinkedItem[] = [];
  const seen = new Set<string>();

  for (const num of Reflect.ownKeys(clozeAccept)) {
    if (typeof num !== 'string') continue;
    const group = clozeAccept[num];
    if (!group) continue;
    for (const canon of Reflect.ownKeys(group)) {
      if (typeof canon !== 'string') continue;
      const key = `${num}:::${canon}`;
      seen.add(key);
      const isLinked = activeGroups.some(
        (g) => String(g.clozeOrdinal) === num && g.canonical === canon
      );
      if (!isLinked) {
        unlinked.push({
          clozeNumber: num,
          canonical: canon,
          alternatives: group[canon] ?? [],
          isInvalid: isEntryInvalid(meta, num, canon),
        });
      }
    }
  }

  if (meta?.invalidEntries) {
    for (const num of Reflect.ownKeys(meta.invalidEntries)) {
      if (typeof num !== 'string') continue;
      const invGroup = meta.invalidEntries[num];
      if (!invGroup) continue;
      for (const canon of Reflect.ownKeys(invGroup)) {
        if (typeof canon !== 'string') continue;
        const key = `${num}:::${canon}`;
        if (seen.has(key)) continue;
        const isLinked = activeGroups.some(
          (g) => String(g.clozeOrdinal) === num && g.canonical === canon
        );
        if (!isLinked) {
          unlinked.push({
            clozeNumber: num,
            canonical: canon,
            alternatives: [],
            isInvalid: true,
          });
        }
      }
    }
  }

  return unlinked;
}

/**
 * Serializes parsed cloze metadata back into a JSON string using null-prototype dictionaries.
 * Unknown root keys, invalid capabilities, invalid groups, and un-repaired entries are preserved untouched.
 */
export function serializeClozeMetadata(meta: ParsedClozeMetadata): string {
  if (!meta.valid && meta.invalidCapability === undefined) {
    return meta.raw;
  }

  const root = createDict<unknown>();

  // Copy unknown root keys
  for (const key of Reflect.ownKeys(meta.unknownRootKeys)) {
    if (typeof key === 'string') {
      setProp(root, key, meta.unknownRootKeys[key]);
    }
  }

  // Preserve invalid capability if present (e.g. false, [])
  if (meta.invalidCapability !== undefined) {
    setProp(root, 'cloze_accept', meta.invalidCapability);
    return JSON.stringify(root);
  }

  const accept = createDict<Record<string, unknown>>();
  setProp(root, 'cloze_accept', accept);

  // Copy invalid groups (e.g. cloze_accept["2"] = "not_an_object")
  for (const numKey of Reflect.ownKeys(meta.invalidGroups)) {
    if (typeof numKey === 'string') {
      setProp(accept, numKey, meta.invalidGroups[numKey] as Record<string, unknown>);
    }
  }

  // Copy active and unlinked alternatives
  for (const numKey of Reflect.ownKeys(meta.clozeAccept)) {
    if (typeof numKey !== 'string') continue;
    // An un-repaired invalid group must NEVER be overwritten by clozeAccept
    if (meta.invalidGroups[numKey] !== undefined) {
      continue;
    }

    const sourceGroup = meta.clozeAccept[numKey];
    if (!sourceGroup) continue;

    let targetGroup = accept[numKey];
    if (!targetGroup || typeof targetGroup !== 'object' || Array.isArray(targetGroup)) {
      targetGroup = createDict<unknown>();
      setProp(accept, numKey, targetGroup);
    }

    for (const canonKey of Reflect.ownKeys(sourceGroup)) {
      if (typeof canonKey !== 'string') continue;
      // If this entry has an un-repaired invalid value, preserve the raw invalid value!
      const invalidVal = meta.invalidEntries[numKey]?.[canonKey];
      if (invalidVal !== undefined) {
        setProp(targetGroup, canonKey, invalidVal);
      } else {
        setProp(targetGroup, canonKey, sourceGroup[canonKey]);
      }
    }
  }

  // Preserve any remaining invalid entries not present in clozeAccept
  for (const numKey of Reflect.ownKeys(meta.invalidEntries)) {
    if (typeof numKey !== 'string') continue;
    // If the group itself was invalid, it was already preserved above
    if (meta.invalidGroups[numKey] !== undefined) continue;

    const invGroup = meta.invalidEntries[numKey];
    if (!invGroup) continue;

    let targetGroup = accept[numKey];
    if (!targetGroup || typeof targetGroup !== 'object' || Array.isArray(targetGroup)) {
      targetGroup = createDict<unknown>();
      setProp(accept, numKey, targetGroup);
    }

    for (const canonKey of Reflect.ownKeys(invGroup)) {
      if (typeof canonKey !== 'string') continue;
      if (targetGroup[canonKey] === undefined) {
        setProp(targetGroup, canonKey, invGroup[canonKey]);
      }
    }
  }

  return JSON.stringify(root);
}

/**
 * Reassociates an unlinked entry to a target active group and removes it from the old location.
 * Does NOT implicitly repair invalid targets or sources.
 */
export function reassociateUnlinked(
  meta: ParsedClozeMetadata,
  unlinkedItem: { clozeNumber: string; canonical: string; alternatives: string[] },
  targetGroup: BlankGroup
): void {
  const targetNum = String(targetGroup.clozeOrdinal);
  const targetCanon = targetGroup.canonical;

  if (isEntryInvalid(meta, unlinkedItem.clozeNumber, unlinkedItem.canonical)) {
    throw new Error('Cannot reassociate an invalid source without repairing it first');
  }

  if (isGroupInvalid(meta, targetNum)) {
    throw new Error('Cannot reassociate into an invalid group without repairing it first');
  }
  if (isEntryInvalid(meta, targetNum, targetCanon)) {
    throw new Error('Cannot reassociate into an invalid target entry without repairing it first');
  }

  let targetDict = meta.clozeAccept[targetNum];
  if (!targetDict) {
    targetDict = createDict<string[]>();
    setProp(meta.clozeAccept, targetNum, targetDict);
  }
  const currentAlts = targetDict[targetCanon] || [];
  const normalizedCurrent = new Set(currentAlts.map(normalizeAnswer));

  for (const alt of unlinkedItem.alternatives) {
    if (!normalizedCurrent.has(normalizeAnswer(alt)) && normalizeAnswer(alt) !== normalizeAnswer(targetCanon)) {
      currentAlts.push(alt);
      normalizedCurrent.add(normalizeAnswer(alt));
    }
  }
  setProp(targetDict, targetCanon, currentAlts);

  // Delete from old position
  const oldGroup = meta.clozeAccept[unlinkedItem.clozeNumber];
  if (oldGroup) {
    delete oldGroup[unlinkedItem.canonical];
    if (Reflect.ownKeys(oldGroup).length === 0) {
      delete meta.clozeAccept[unlinkedItem.clozeNumber];
    }
  }
  const invGroup = meta.invalidEntries[unlinkedItem.clozeNumber];
  if (invGroup) {
    delete invGroup[unlinkedItem.canonical];
    if (Reflect.ownKeys(invGroup).length === 0) {
      delete meta.invalidEntries[unlinkedItem.clozeNumber];
    }
  }
}

/**
 * Explicitly removes an unlinked entry.
 */
export function deleteUnlinked(
  meta: ParsedClozeMetadata,
  unlinkedItem: { clozeNumber: string; canonical: string; alternatives: string[] }
): void {
  const oldGroup = meta.clozeAccept[unlinkedItem.clozeNumber];
  if (oldGroup) {
    delete oldGroup[unlinkedItem.canonical];
    if (Reflect.ownKeys(oldGroup).length === 0) {
      delete meta.clozeAccept[unlinkedItem.clozeNumber];
    }
  }
  const invGroup = meta.invalidEntries[unlinkedItem.clozeNumber];
  if (invGroup) {
    delete invGroup[unlinkedItem.canonical];
  }
}

/**
 * Normalizes text for comparison, matching Go backend normalizeAnswer (lowercase and collapsed whitespace).
 */
export function normalizeAnswer(text: string): string {
  return text.trim().toLowerCase().replace(/\s+/g, ' ');
}
