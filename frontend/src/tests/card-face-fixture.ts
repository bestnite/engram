import type { CardFace } from '../lib/api';

/**
 * Test helper for the face objects the review and preview responses carry.
 *
 * Most cases care only about the HTML reaching the frame, so the note type's CSS and body class default to the values a
 * card without custom styling produces. A case that asserts the note type's own CSS or class list overrides them.
 */

/** One face shaped like the server's `front` / `back` object. */
export function face(html: string, overrides: Partial<CardFace> = {}): CardFace {
  return { html, css: '', body_class: 'card', side: 'front', media_base: '', ...overrides };
}

/** Both faces of one card; every response writes the same media base on each face. */
export function faces(frontHTML: string, backHTML: string, mediaBase = ''): { front: CardFace; back: CardFace } {
  return {
    front: face(frontHTML, { media_base: mediaBase }),
    back: face(backHTML, { side: 'back', media_base: mediaBase }),
  };
}
