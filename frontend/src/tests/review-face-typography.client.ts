import { describe, it, expect, afterEach } from 'vitest';
import { REVIEW_ANSWER_CSS, REVIEW_BACK_CSS, REVIEW_FRONT_CSS, faceStyle } from '../lib/card-frame';

/**
 * The review page's default type scale, computed from the CSS each frame receives.
 *
 * The question, the revealed answer and a graded result's answer are three different faces of the same card, so a learner
 * who reveals or grades a card must not see its text change size. The default belongs to the body element, which the note
 * type's own CSS can outrank; the content root must only inherit it.
 *
 * happy-dom resolves the cascade, media queries and specificity for these rules, so the cases read the computed values a
 * frame would use rather than the shape of the CSS string. It is not a browser engine; the real-browser counterpart of this
 * contract lives in frontend/e2e/review-face-style.spec.ts. The width cases are the two sides of the front's `640px`
 * breakpoint: the `min-width:640px` query must raise all three faces together, and below it all three keep one size.
 */

const FACES = [
  { name: 'front', css: REVIEW_FRONT_CSS },
  { name: 'revealed back', css: REVIEW_BACK_CSS },
  { name: 'graded answer', css: REVIEW_ANSWER_CSS },
];

// The front's established scale, in the units a computed style reports.
const NARROW_FONT_SIZE = '20px'; // 1.25rem
const WIDE_FONT_SIZE = '24px'; // 1.5rem
const LINE_HEIGHT = '1.625';

interface ComputedFace {
  bodyFontSize: string;
  qaFontSize: string;
  bodyLineHeight: string;
  bodyFontWeight: string;
}

function setWidth(width: number): void {
  (window as unknown as { happyDOM: { setViewport: (size: { width: number; height: number }) => void } }).happyDOM.setViewport({
    width,
    height: 800,
  });
}

/**
 * Writes the face CSS the way the frame does and reads the computed values of the body and of the content root.
 *
 * `noteCSS` is the note type's own CSS, which `faceStyle` appends after the engram defaults exactly as the frame receives it.
 */
function renderFace(css: string, noteCSS = ''): ComputedFace {
  document.head.innerHTML = `<style>${faceStyle(css, noteCSS)}</style>`;
  document.body.className = 'card';
  document.body.innerHTML = '<div id="qa"><p>face</p></div>';

  const body = getComputedStyle(document.body);
  const content = getComputedStyle(document.getElementById('qa')!);
  return {
    bodyFontSize: body.fontSize,
    qaFontSize: content.fontSize,
    bodyLineHeight: body.lineHeight,
    bodyFontWeight: body.fontWeight,
  };
}

afterEach(() => {
  document.head.innerHTML = '';
  document.body.className = '';
  document.body.innerHTML = '';
  setWidth(1024);
});

describe('review face typography', () => {
  it('gives the front, the revealed back and a graded answer one default size on both sides of the breakpoint', () => {
    for (const width of [480, 1024]) {
      setWidth(width);
      const expected = width < 640 ? NARROW_FONT_SIZE : WIDE_FONT_SIZE;
      for (const face of FACES) {
        const computed = renderFace(face.css);
        // Soft assertions so one face that drifts does not hide the others in the same run.
        expect.soft(computed.bodyFontSize, `${face.name} body at ${width}px`).toBe(expected);
        expect.soft(computed.qaFontSize, `${face.name} content at ${width}px`).toBe(expected);
        expect.soft(computed.bodyLineHeight, `${face.name} leading at ${width}px`).toBe(LINE_HEIGHT);
      }
    }
  });

  it('keeps the graded answer at medium weight and the ungraded faces at the normal weight', () => {
    const weights = Object.fromEntries(FACES.map((face) => [face.name, renderFace(face.css).bodyFontWeight]));
    expect(weights).toEqual({ front: 'normal', 'revealed back': 'normal', 'graded answer': '500' });
  });

  it('lets a note type rule outrank the default at any width, including a later rule of equal specificity', () => {
    for (const width of [480, 1024]) {
      setWidth(width);
      for (const face of FACES) {
        // A class rule outranks `:where(body)`, which carries no specificity, and the content root inherits the result.
        const byClass = renderFace(face.css, '.card { font-size: 16px; }');
        expect(byClass.bodyFontSize, `${face.name} class override at ${width}px`).toBe('16px');
        expect(byClass.qaFontSize, `${face.name} class override inherited at ${width}px`).toBe('16px');

        // The same selector wins when the note type supplies it later, because the note type CSS is appended last.
        const byOrder = renderFace(face.css, ':where(body) { font-size: 17px; }');
        expect(byOrder.bodyFontSize, `${face.name} later equal rule at ${width}px`).toBe('17px');
        expect(byOrder.qaFontSize, `${face.name} later equal rule inherited at ${width}px`).toBe('17px');
      }
    }
  });

  it('styles the body through a zero-specificity selector and never the content root', () => {
    for (const face of FACES) {
      expect(face.css, face.name).toContain(':where(body)');
      expect(face.css, face.name).not.toContain('#qa');
      expect(face.css, face.name).not.toContain('.card');
    }
  });

  it('appends the note type CSS after the engram defaults', () => {
    const noteCSS = '.card { font-size: 16px; }';
    expect(faceStyle(REVIEW_FRONT_CSS, noteCSS)).toBe(`${REVIEW_FRONT_CSS}\n${noteCSS}`);
    expect(faceStyle(REVIEW_BACK_CSS, noteCSS)).toBe(`${REVIEW_BACK_CSS}\n${noteCSS}`);
    expect(faceStyle(REVIEW_ANSWER_CSS, noteCSS)).toBe(`${REVIEW_ANSWER_CSS}\n${noteCSS}`);
  });
});
