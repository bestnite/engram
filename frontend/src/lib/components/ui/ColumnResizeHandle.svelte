<script lang="ts">
  /**
   * The divider on the left edge of a fixed-width list column, dragged to resize that column.
   *
   * The lists that use it put one flexible column first and right-align the fixed columns after it, so a column's left
   * edge is the divider the user grabs: moving it left by some pixels widens the column by the same amount and the
   * flexible column gives the space up. The divider therefore follows the pointer. Arrow keys move it in 16px steps for
   * keyboard users, and a double click or Home restores the column's default width.
   */
  interface Props {
    /** Current width in px. */
    width: number;
    min: number;
    max: number;
    /** Accessible name, naming the column the divider resizes. */
    label: string;
    /** Receives every new width while the divider moves. */
    onresize: (width: number) => void;
    /** Called once when a drag or a key press ends, so the caller can persist the width. */
    oncommit?: () => void;
    /** Restores the default width. */
    onreset: () => void;
    testId?: string;
  }

  let { width, min, max, label, onresize, oncommit, onreset, testId }: Props = $props();

  const KEY_STEP = 16;

  let dragStartX = 0;
  let dragStartWidth = 0;
  let dragging = $state(false);

  function clamp(value: number): number {
    return Math.round(Math.min(max, Math.max(min, value)));
  }

  function onPointerDown(event: PointerEvent): void {
    if (event.button !== 0) return;
    event.preventDefault();
    dragStartX = event.clientX;
    dragStartWidth = width;
    dragging = true;
    (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
  }

  function onPointerMove(event: PointerEvent): void {
    if (!dragging) return;
    onresize(clamp(dragStartWidth - (event.clientX - dragStartX)));
  }

  function onPointerUp(event: PointerEvent): void {
    if (!dragging) return;
    dragging = false;
    (event.currentTarget as HTMLElement).releasePointerCapture(event.pointerId);
    oncommit?.();
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
      event.preventDefault();
      onresize(clamp(width + (event.key === 'ArrowLeft' ? KEY_STEP : -KEY_STEP)));
      oncommit?.();
    } else if (event.key === 'Home') {
      event.preventDefault();
      onreset();
    }
  }
</script>

<!-- A focusable separator is the ARIA pattern for a splitter; aria-valuenow carries the width it controls. The static
     check counts every separator as non-interactive, while a focusable one with a value is interactive by that pattern. -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
<div
  role="separator"
  aria-orientation="vertical"
  aria-label={label}
  aria-valuenow={width}
  aria-valuemin={min}
  aria-valuemax={max}
  tabindex="0"
  title={label}
  data-testid={testId}
  class="group/resize absolute inset-y-0 -left-3 z-10 flex w-3 cursor-col-resize touch-none select-none justify-center outline-none"
  onpointerdown={onPointerDown}
  onpointermove={onPointerMove}
  onpointerup={onPointerUp}
  onpointercancel={onPointerUp}
  ondblclick={onreset}
  onkeydown={onKeydown}
>
  <span
    class="my-2 w-px bg-border transition-colors group-hover/resize:bg-ring group-focus-visible/resize:bg-ring {dragging ? 'bg-ring' : ''}"
    aria-hidden="true"
  ></span>
</div>
