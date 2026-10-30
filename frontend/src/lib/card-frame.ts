/**
 * Card-face CSS sent with the frame's `show` message.
 *
 * The frame document loads the application stylesheet, so design tokens and the utility classes the audio and speech
 * controls carry already exist there. These strings only restate the typography each view used to apply to the wrapper element
 * around the face, because that wrapper stays in the parent and its classes no longer reach the content.
 *
 * Every rule targets `#qa`, which is the element the frame runtime writes the card HTML into.
 */

const FACE_BASE = 'color:var(--foreground);white-space:pre-wrap;overflow-wrap:break-word;';

/** The review page's front face: `text-xl sm:text-2xl`, relaxed leading. */
export const REVIEW_FRONT_CSS = `#qa{${FACE_BASE}font-size:1.25rem;line-height:1.625}@media (min-width:640px){#qa{font-size:1.5rem}}`;

/** The review page's revealed answer and the heading above it: `text-lg`, relaxed leading. */
export const REVIEW_BACK_CSS = `#qa{${FACE_BASE}font-size:1.125rem;line-height:1.625}`;

/** A graded answer face: `text-lg`, medium weight, as the result panel showed it. */
export const REVIEW_ANSWER_CSS = `#qa{${FACE_BASE}font-size:1.125rem;line-height:1.625;font-weight:500}`;

/**
 * The note editor preview: the face keeps the page's default type scale and normal white-space handling, as the preview
 * wrapper did (only the review faces preserved source line breaks).
 */
export const NOTE_PREVIEW_CSS = '#qa{color:var(--foreground);overflow-wrap:break-word}';

/**
 * The style string a frame receives: the view's engram face constant first, then the note type's own CSS.
 *
 * The order is the rule, not a preference: the frame document writes the whole string into one `<style>` element, so a note
 * type rule that targets the same selector wins over the engram default only when it comes later. A face without CSS (or
 * before the render response lands) keeps the engram typography alone.
 */
export function faceStyle(base: string, css: string | null | undefined): string {
  const noteTypeCSS = css?.trim() ?? '';
  return noteTypeCSS === '' ? base : `${base}\n${noteTypeCSS}`;
}
