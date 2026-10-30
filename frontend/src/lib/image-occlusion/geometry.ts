/**
 * Conversion between the canvas objects the editor drags and the normalized geometry the field stores.
 *
 * The canvas works in device pixels of the background image; the field stores fractions of the image's
 * width and height, so a mask keeps its place when the note is reviewed at another size. Keeping this
 * conversion in one module lets it be exercised with plain objects: the drawing library is only needed
 * to move a shape with a pointer, not to decide what a moved shape means.
 *
 * Fabric rotates and scales an object around its centre, so the centre point is what the conversion
 * starts from. Deriving the top-left of a resized rectangle from `left` alone would drift by half the
 * size change on every resize.
 */

import type { OcclusionGeometry, OcclusionPoint, OcclusionShapeKind } from './markup';

/** A canvas size in pixels. */
export interface CanvasSize {
  width: number;
  height: number;
}

/** A point in canvas pixels. */
export interface CanvasPoint {
  x: number;
  y: number;
}

/**
 * The part of a drawing object this module reads. It matches the fields a fabric rectangle, ellipse,
 * polygon and text expose, without importing the library into the model.
 */
export interface ShapeObject {
  /** The object's centre in canvas pixels, as fabric's `getCenterPoint()` reports it. */
  center: CanvasPoint;
  width: number;
  height: number;
  scaleX: number;
  scaleY: number;
  angle: number;
  rx?: number;
  ry?: number;
  points?: Array<{ x: number; y: number }>;
  pathOffset?: { x: number; y: number };
  text?: string;
  fontSize?: number;
}

function fraction(value: number, size: number): number {
  return size > 0 && Number.isFinite(value) ? value / size : 0;
}

/**
 * The absolute pixel coordinates of a polygon's vertices. Fabric stores vertices relative to the
 * centre of their bounding box and scales them around that centre, so both have to be undone here.
 */
export function polygonPoints(object: ShapeObject): OcclusionPoint[] {
  const offset = object.pathOffset ?? { x: 0, y: 0 };
  return (object.points ?? []).map((point) => ({
    x: object.center.x + (point.x - offset.x) * object.scaleX,
    y: object.center.y + (point.y - offset.y) * object.scaleY,
  }));
}

/** Reads one dragged object as normalized geometry. Rotation is returned in degrees. */
export function geometryFromObject(kind: OcclusionShapeKind, object: ShapeObject, size: CanvasSize): OcclusionGeometry {
  const center = object.center;
  const scaleX = object.scaleX || 1;
  const scaleY = object.scaleY || 1;
  const width = (object.width ?? 0) * scaleX;
  const height = (object.height ?? 0) * scaleY;
  const geometry: OcclusionGeometry = {
    left: fraction(center.x - width / 2, size.width),
    top: fraction(center.y - height / 2, size.height),
    angle: Number.isFinite(object.angle) ? object.angle : 0,
  };
  switch (kind) {
    case 'rect':
      geometry.width = fraction(width, size.width);
      geometry.height = fraction(height, size.height);
      break;
    case 'ellipse': {
      const rx = (object.rx ?? 0) * scaleX;
      const ry = (object.ry ?? 0) * scaleY;
      geometry.left = fraction(center.x - rx, size.width);
      geometry.top = fraction(center.y - ry, size.height);
      geometry.rx = fraction(rx, size.width);
      geometry.ry = fraction(ry, size.height);
      break;
    }
    case 'polygon': {
      const points = polygonPoints(object);
      const first = points[0];
      if (!first) break;
      let left = first.x;
      let top = first.y;
      for (const point of points) {
        if (point.x < left) left = point.x;
        if (point.y < top) top = point.y;
      }
      geometry.left = fraction(left, size.width);
      geometry.top = fraction(top, size.height);
      geometry.points = points.map((point) => ({ x: fraction(point.x, size.width), y: fraction(point.y, size.height) }));
      break;
    }
    case 'text':
      geometry.text = object.text ?? '';
      geometry.scale = scaleX;
      geometry.fontSize = fraction((object.fontSize ?? 0) * scaleY, size.height);
      break;
  }
  return geometry;
}
