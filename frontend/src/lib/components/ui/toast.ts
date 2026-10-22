/**
 * The single entry point for toasts: views get toast from here and do not depend on svelte-sonner directly,
 * so swapping the implementation later touches only this file. The mount point is Toaster in App.svelte.
 *
 * Request feedback has only two shapes, consistent site-wide:
 * - In progress: the button that triggered the request shows loading (spinner, disabled, text unchanged), see ui/Button.svelte.
 * - Result: toast.success on success and toast.error on failure, with no line of text inserted next to the button and no button text change.
 *
 * Only three kinds stay in place and never use toast, because the user must handle them where they are:
 * - A failed submit inside an open dialog: the error shows inside the dialog and the user fixes the input and retries there.
 * - Pre-submit field validation errors: attached below the corresponding field.
 * - A whole-page load failure: the page has no content to show, so a full-page error state is rendered.
 */
export { toast } from 'svelte-sonner';
