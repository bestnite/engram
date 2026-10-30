<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { t } from '../i18n';
  import {
    assignOrdinal,
    createShape,
    documentMode,
    documentShapes,
    parseOcclusionField,
    serializeOcclusionField,
    setDocumentMode,
    setShapeGeometry,
    setShapeOccludeInactive,
    shapeGeometry,
    type OcclusionDocument,
    type OcclusionGeometry,
    type OcclusionMode,
    type OcclusionShape,
    type OcclusionShapeKind,
  } from '../image-occlusion/markup';
  import { geometryFromObject, type ShapeObject } from '../image-occlusion/geometry';

  /**
   * The mask editor of an image-occlusion note.
   *
   * A note stores one deletion per shape, so the canvas is a view of the field value rather than a
   * separate document: every shape change is written straight back through `onchange`, and nothing is
   * normalized until the user actually changes a shape. That is what makes reopening a note and saving
   * it unchanged a no-op on the stored bytes.
   *
   * The drawing library is loaded on mount, never during SSR. A client without a canvas (the test
   * environment) still gets the model controls, because adding, grouping and deleting a shape only
   * changes the field and does not need a working paint surface.
   */

  interface Props {
    /** The current value of the note type's occlusion field. */
    value: string;
    /** URL of the note's first image, drawn under the masks. */
    imageSrc?: string;
    /** Called with the complete field value whenever a shape or a group changes. */
    onchange?: (value: string) => void;
  }

  let { value, imageSrc = '', onchange }: Props = $props();

  const tools: OcclusionShapeKind[] = ['rect', 'ellipse', 'polygon', 'text'];
  const SHAPE_MASK_COLOR = '#ffeba2';
  const SHAPE_STROKE = '#212121';
  const TEXT_COLOR = '#000000';
  const TEXT_FONT_SIZE = 40;
  const FALLBACK_SIZE = { width: 800, height: 600 };

  let document_ = $state<OcclusionDocument>({ parts: [] });
  let cloze = $state(1);
  let mode = $state<OcclusionMode>('hideOne');
  let chosen = $state<string[]>([]);
  let natural = $state({ width: 0, height: 0 });
  let canvasAvailable = $state(false);

  let lastEmitted = '';
  let canvasEl = $state<HTMLCanvasElement | null>(null);
  let imageEl = $state<HTMLImageElement | null>(null);
  let canvas: import('fabric').Canvas | null = null;
  let fabric: typeof import('fabric') | null = null;
  let disposed = false;

  const shapes = $derived(documentShapes(document_));
  const canvasSize = $derived(
    natural.width > 0 && natural.height > 0 ? { width: natural.width, height: natural.height } : FALLBACK_SIZE,
  );

  /** The field value, emitted once per change. */
  function emit(): void {
    const next = serializeOcclusionField(document_);
    lastEmitted = next;
    onchange?.(next);
  }

  /** Re-reads the field because the parent replaced it (a reopened note, or a note type change). */
  function reload(next: string): void {
    document_ = parseOcclusionField(next);
    mode = documentMode(document_);
    chosen = [];
    lastEmitted = next;
    rebuildCanvas();
  }

  $effect(() => {
    const next = value;
    if (next === lastEmitted) return;
    untrack(() => reload(next));
  });

  function shapeById(id: string): OcclusionShape | undefined {
    return shapes.find((shape) => shape.id === id);
  }

  /** The shapes an action applies to: the chosen ones, or the last shape when nothing is chosen. */
  function targets(): OcclusionShape[] {
    const picked = chosen.map((id) => shapeById(id)).filter((shape): shape is OcclusionShape => shape !== undefined);
    if (picked.length > 0) return picked;
    const last = shapes[shapes.length - 1];
    return last ? [last] : [];
  }

  /** A placement that does not stack every new shape on the same spot. */
  function defaultGeometry(kind: OcclusionShapeKind, index: number): OcclusionGeometry {
    const offset = (index % 6) * 0.03;
    switch (kind) {
      case 'rect':
        return { left: 0.1 + offset, top: 0.1 + offset, width: 0.2, height: 0.12 };
      case 'ellipse':
        return { left: 0.55 + offset, top: 0.1 + offset, rx: 0.1, ry: 0.08 };
      case 'polygon':
        return {
          left: 0.1 + offset,
          top: 0.5 + offset,
          points: [
            { x: 0.1 + offset, y: 0.5 + offset },
            { x: 0.28 + offset, y: 0.5 + offset },
            { x: 0.19 + offset, y: 0.64 + offset },
          ],
        };
      case 'text':
        return { left: 0.55 + offset, top: 0.5 + offset, text: '', scale: 1, fontSize: 0.06 };
    }
  }

  /** Adds one shape of the chosen kind and assigns it to the current cloze number. */
  function addShape(kind: OcclusionShapeKind): void {
    const shape = createShape(kind, Math.max(1, cloze), defaultGeometry(kind, shapes.length));
    if (mode === 'hideAll') setShapeOccludeInactive(shape, true);
    document_.parts.push({ type: 'shape', shape });
    chosen = [shape.id];
    emit();
    rebuildCanvas();
  }

  /** Puts every chosen shape (or the last one) under the same cloze number, which is how a group is made. */
  function applyCloze(): void {
    const picked = targets();
    if (picked.length === 0) return;
    for (const shape of picked) assignOrdinal(shape, Math.max(1, cloze));
    emit();
    rebuildCanvas();
  }

  function removeShape(id: string): void {
    document_.parts = document_.parts.filter((part) => part.type !== 'shape' || part.shape.id !== id);
    chosen = chosen.filter((current) => current !== id);
    emit();
    rebuildCanvas();
  }

  function toggleShape(id: string): void {
    chosen = chosen.includes(id) ? chosen.filter((current) => current !== id) : [...chosen, id];
    if (!canvas) return;
    const object = canvas.getObjects().find((item) => (item as FabricTagged).occlusionShapeId === id);
    if (!object) return;
    if (chosen.includes(id)) {
      const active = canvas.getActiveObjects();
      if (active.includes(object)) return;
      canvas.setActiveObject(object);
    } else {
      canvas.discardActiveObject();
    }
    canvas.requestRenderAll();
  }

  function changeMode(next: string): void {
    mode = next === 'hideAll' ? 'hideAll' : 'hideOne';
    setDocumentMode(document_, mode);
    emit();
    rebuildCanvas();
  }

  type FabricTagged = import('fabric').FabricObject & { occlusionShapeId?: string };

  /** Adapts a fabric object to the plain shape the conversion module reads. */
  function asShapeObject(object: import('fabric').FabricObject): ShapeObject {
    const padded = object as unknown as {
      width?: number;
      height?: number;
      rx?: number;
      ry?: number;
      points?: Array<{ x: number; y: number }>;
      pathOffset?: { x: number; y: number };
      text?: string;
      fontSize?: number;
    };
    return {
      center: object.getCenterPoint(),
      width: padded.width ?? 0,
      height: padded.height ?? 0,
      scaleX: object.scaleX,
      scaleY: object.scaleY,
      angle: object.angle,
      rx: padded.rx,
      ry: padded.ry,
      points: padded.points,
      pathOffset: padded.pathOffset,
      text: padded.text,
      fontSize: padded.fontSize,
    };
  }

  /** Builds the canvas object of one stored shape, in the canvas's pixel space. */
  function objectFor(shape: OcclusionShape): import('fabric').FabricObject | null {
    if (!fabric) return null;
    const { width, height } = canvasSize;
    const geometry = shapeGeometry(shape);
    const fill = geometry.fill ?? SHAPE_MASK_COLOR;
    const common = {
      angle: geometry.angle ?? 0,
      fill,
      stroke: SHAPE_STROKE,
      strokeWidth: 1,
      cornerColor: SHAPE_STROKE,
      cornerStrokeColor: SHAPE_STROKE,
      transparentCorners: false,
    };
    let object: import('fabric').FabricObject;
    switch (shape.kind) {
      case 'rect': {
        const w = (geometry.width ?? 0) * width;
        const h = (geometry.height ?? 0) * height;
        object = new fabric.Rect({
          ...common,
          left: geometry.left * width + w / 2,
          top: geometry.top * height + h / 2,
          width: w,
          height: h,
          originX: 'center',
          originY: 'center',
        });
        break;
      }
      case 'ellipse': {
        const rx = (geometry.rx ?? 0) * width;
        const ry = (geometry.ry ?? 0) * height;
        object = new fabric.Ellipse({
          ...common,
          left: geometry.left * width + rx,
          top: geometry.top * height + ry,
          rx,
          ry,
          originX: 'center',
          originY: 'center',
        });
        break;
      }
      case 'polygon': {
        const absolute = (geometry.points ?? []).map((point) => ({ x: point.x * width, y: point.y * height }));
        const first = absolute[0];
        if (!first) return null;
        let minX = first.x;
        let minY = first.y;
        for (const point of absolute) {
          if (point.x < minX) minX = point.x;
          if (point.y < minY) minY = point.y;
        }
        const relative = absolute.map((point) => ({ x: point.x - minX, y: point.y - minY }));
        const polygon = new fabric.Polygon(relative, common);
        // The constructor puts the object at the centre of its own vertices; move that centre onto
        // the image and the stored absolute points are reproduced exactly.
        polygon.set({ left: polygon.left + minX, top: polygon.top + minY });
        polygon.setCoords();
        object = polygon;
        break;
      }
      case 'text': {
        const fontSize = (geometry.fontSize ?? TEXT_FONT_SIZE / height) * height;
        object = new fabric.Text(geometry.text ?? '', {
          ...common,
          fill: geometry.fill ?? TEXT_COLOR,
          strokeWidth: 0,
          left: geometry.left * width,
          top: geometry.top * height,
          fontSize,
          originX: 'left',
          originY: 'top',
        });
        break;
      }
    }
    (object as FabricTagged).occlusionShapeId = shape.id;
    return object;
  }

  /** Replaces the canvas contents with the current model. Cheap enough for a shape list of mask size. */
  function rebuildCanvas(): void {
    if (!canvas) return;
    canvas.getObjects().slice().forEach((object) => canvas?.remove(object));
    for (const shape of shapes) {
      const object = objectFor(shape);
      if (object) canvas.add(object);
    }
    canvas.requestRenderAll();
  }

  /** Writes a dragged, resized or rotated object back into its stored shape. */
  function onObjectModified(event: { target?: import('fabric').FabricObject }): void {
    const object = event.target as FabricTagged | undefined;
    if (!object?.occlusionShapeId) return;
    const shape = shapeById(object.occlusionShapeId);
    if (!shape) return;
    setShapeGeometry(shape, geometryFromObject(shape.kind, asShapeObject(object), canvasSize));
    emit();
  }

  function syncSelection(): void {
    if (!canvas) return;
    chosen = canvas
      .getActiveObjects()
      .map((object) => (object as FabricTagged).occlusionShapeId)
      .filter((id): id is string => id !== undefined);
  }

  function resizeCanvas(): void {
    if (!canvas) return;
    canvas.setDimensions({ width: canvasSize.width, height: canvasSize.height });
    canvas.requestRenderAll();
  }

  function onImageLoad(): void {
    if (!imageEl) return;
    const width = imageEl.naturalWidth;
    const height = imageEl.naturalHeight;
    if (width > 0 && height > 0) {
      natural = { width, height };
      resizeCanvas();
      rebuildCanvas();
    }
  }

  onMount(() => {
    void (async () => {
      try {
        const module = await import('fabric');
        if (disposed || !canvasEl) return;
        fabric = module;
        canvas = new module.Canvas(canvasEl, {
          width: canvasSize.width,
          height: canvasSize.height,
          selection: true,
          preserveObjectStacking: true,
        });
        canvas.on('object:modified', onObjectModified);
        canvas.on('selection:created', syncSelection);
        canvas.on('selection:updated', syncSelection);
        canvas.on('selection:cleared', syncSelection);
        canvasAvailable = true;
        rebuildCanvas();
      } catch {
        // A canvas that cannot be created leaves the model controls usable; the field stays editable.
        canvasAvailable = false;
      }
    })();
    return () => {
      disposed = true;
      canvas?.dispose();
      canvas = null;
    };
  });
</script>

<section data-testid="occlusion-editor" class="space-y-3 rounded-lg border border-border bg-surface p-4">
  <div class="flex items-baseline justify-between gap-2">
    <h3 class="text-sm font-semibold text-foreground">{$t('occlusion.title')}</h3>
    <span data-testid="occlusion-shape-count" class="text-xs text-muted-foreground">{$t('occlusion.shape_count', { count: shapes.length })}</span>
  </div>
  <p class="text-xs text-muted-foreground">{$t('occlusion.hint')}</p>

  {#if !imageSrc}
    <p data-testid="occlusion-no-image" class="text-xs text-warning-foreground">{$t('occlusion.no_image')}</p>
  {/if}

  <div
    class="relative overflow-hidden rounded-md border border-border bg-muted"
    style={natural.width > 0 && natural.height > 0 ? `aspect-ratio: ${natural.width} / ${natural.height}` : 'aspect-ratio: 4 / 3'}
    data-testid="occlusion-stage"
  >
    {#if imageSrc}
      <!-- The background is a plain image, so stored HTML stays out of the page; only its URL is used. -->
      <img
        bind:this={imageEl}
        src={imageSrc}
        alt=""
        class="absolute inset-0 size-full object-contain select-none"
        draggable="false"
        onload={onImageLoad}
      />
    {/if}
    <canvas bind:this={canvasEl} data-testid="occlusion-canvas" class="absolute inset-0 size-full"></canvas>
    {#if !canvasAvailable}
      <p data-testid="occlusion-canvas-fallback" class="absolute inset-x-0 bottom-0 bg-surface/90 p-1 text-center text-[11px] text-muted-foreground">
        {$t('occlusion.canvas_unavailable')}
      </p>
    {/if}
  </div>

  <div role="toolbar" aria-label={$t('occlusion.tools')} class="flex flex-wrap gap-2">
    {#each tools as kind}
      <button
        type="button"
        data-testid={`occlusion-add-${kind}`}
        class="inline-flex cursor-pointer items-center rounded-md border border-border bg-background px-2.5 py-1 text-xs text-foreground hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
        onclick={() => addShape(kind)}
      >
        {$t(`occlusion.tool.${kind}`)}
      </button>
    {/each}
  </div>

  <div class="flex flex-wrap items-end gap-3">
    <label class="text-xs font-medium text-foreground">
      {$t('occlusion.cloze')}
      <input
        type="number"
        min="1"
        step="1"
        data-testid="occlusion-cloze"
        bind:value={cloze}
        class="field-input mt-1 block w-20 text-sm"
      />
    </label>
    <button
      type="button"
      data-testid="occlusion-apply-cloze"
      class="inline-flex cursor-pointer items-center rounded-md border border-border bg-background px-2.5 py-1.5 text-xs text-foreground hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
      onclick={applyCloze}
    >
      {$t('occlusion.apply_cloze')}
    </button>
    <label class="text-xs font-medium text-foreground">
      {$t('occlusion.mode.label')}
      <select
        data-testid="occlusion-mode"
        class="field-input mt-1 block text-sm"
        value={mode}
        onchange={(event) => changeMode(event.currentTarget.value)}
      >
        <option value="hideOne">{$t('occlusion.mode.hide_one')}</option>
        <option value="hideAll">{$t('occlusion.mode.hide_all')}</option>
      </select>
    </label>
  </div>
  <p class="text-xs text-muted-foreground">{$t('occlusion.mode.hint')}</p>

  {#if shapes.length === 0}
    <p data-testid="occlusion-empty" class="text-xs text-muted-foreground">{$t('occlusion.empty')}</p>
  {:else}
    <ul class="space-y-1" aria-label={$t('occlusion.shapes')}>
      {#each shapes as shape, index (shape.id)}
        <li class="flex items-center gap-2">
          <button
            type="button"
            data-testid={`occlusion-shape-${index}`}
            aria-pressed={chosen.includes(shape.id)}
            class="min-w-0 flex-1 cursor-pointer truncate rounded-md border border-border px-2 py-1 text-left text-xs hover:bg-muted aria-pressed:border-ring aria-pressed:bg-muted"
            onclick={() => toggleShape(shape.id)}
          >
            {$t('occlusion.shape', { ordinal: shape.ordinal, kind: $t(`occlusion.tool.${shape.kind}`) })}
          </button>
          <button
            type="button"
            data-testid={`occlusion-remove-${index}`}
            class="cursor-pointer rounded-md border border-border px-2 py-1 text-xs text-destructive-foreground hover:bg-muted"
            aria-label={$t('occlusion.remove')}
            title={$t('occlusion.remove')}
            onclick={() => removeShape(shape.id)}
          >
            ×
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</section>
