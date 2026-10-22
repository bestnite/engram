import { writable } from 'svelte/store';

/**
 * Site-wide second confirmation: replaces the browser's native window.confirm.
 *
 * The native confirm box's styling and button text are decided by the browser and system language, inconsistent with the app's dialogs, and it has no enter/exit animation.
 * Calling askConfirm returns a Promise: true when the user confirms, false when they cancel or close.
 * Only one confirm box exists at a time; the mount point is ConfirmHost in App.svelte.
 */
export interface ConfirmOptions {
  title: string;
  description?: string;
  /** Confirm button text; defaults to "OK". */
  confirmLabel?: string;
  /** Dangerous actions (delete, revoke, disable): the confirm button is red. */
  destructive?: boolean;
}

export interface ConfirmRequest extends ConfirmOptions {
  resolve: (ok: boolean) => void;
}

export const confirmRequest = writable<ConfirmRequest | null>(null);

export function askConfirm(options: ConfirmOptions): Promise<boolean> {
  return new Promise((resolve) => {
    confirmRequest.update((pending) => {
      // When a confirmation is already unanswered, cancel it first so the old Promise is not left pending forever.
      pending?.resolve(false);
      return { ...options, resolve };
    });
  });
}
