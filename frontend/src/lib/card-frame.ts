/**
 * Card-face CSS sent with the frame's `show` message.
 *
 * The frame document loads the application stylesheet, so design tokens and the utility classes the audio and speech
 * controls carry already exist there. These strings only restate the typography each view used to apply to the wrapper element
 * around the face, because that wrapper stays in the parent and its classes no longer reach the content.
 *
 * The note type's CSS follows these strings and outranks them; see FACE_BASE.
 */

// Every rule targets `:where(body)`, which carries no specificity. The frame writes the card's classes (`card`, `cardN`)
// on its body, so a note type rule such as `.card { font-size: 16px }` always wins, and these values only fill in what
// the note type leaves unset. They must not target `#qa`: a property set on the content root beats the value it would
// inherit from `.card`, which is how a deck's own font size used to be overridden. Source line breaks are not preserved:
// a field and a template are HTML, so the newlines and indentation between their tags are not content.
const FACE_BASE = 'overflow-wrap:break-word;';

/**
 * The review page's default type scale: the front's `text-xl sm:text-2xl` with relaxed leading.
 *
 * The question, the revealed answer and a graded result's answer all use this scale, so a learner who reveals or grades a
 * card sees its text keep the same size. The front and the answer must not drift apart: a smaller answer face makes the
 * card jump when it is revealed.
 */
const REVIEW_TYPE_SCALE = `${FACE_BASE}font-size:1.25rem;line-height:1.625`;
const REVIEW_WIDE_TYPE_SCALE = '@media (min-width:640px){:where(body){font-size:1.5rem}}';

/** The review page's front face: the shared review type scale. */
export const REVIEW_FRONT_CSS = `:where(body){${REVIEW_TYPE_SCALE}}${REVIEW_WIDE_TYPE_SCALE}`;

/** The review page's revealed answer: the shared review type scale, so revealing does not resize the card. */
export const REVIEW_BACK_CSS = `:where(body){${REVIEW_TYPE_SCALE}}${REVIEW_WIDE_TYPE_SCALE}`;

/** A graded answer face: the shared review type scale with the result panel's medium weight. */
export const REVIEW_ANSWER_CSS = `:where(body){${REVIEW_TYPE_SCALE};font-weight:500}${REVIEW_WIDE_TYPE_SCALE}`;

/** The note editor preview: the face keeps the page's default type scale. */
export const NOTE_PREVIEW_CSS = `:where(body){${FACE_BASE}}`;

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
