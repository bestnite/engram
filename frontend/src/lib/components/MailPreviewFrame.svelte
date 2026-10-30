<script lang="ts">
  /**
   * MailPreviewFrame shows one rendered mail inside a sandboxed iframe.
   *
   * The mail is a complete HTML document: the server renders the body, substitutes the variables and
   * wraps the result in the fixed shell, so the preview must render the whole document, not a
   * fragment. A dedicated frame is used instead of CardFrame because CardFrame is the Anki reviewer
   * runtime (it owns `show {html, css}` for one card face); a mail is not a card face and its shell
   * would be nested in the card document.
   *
   * `sandbox` carries no `allow-scripts`: an administrator writes arbitrary HTML, and a preview must
   * never execute it in the parent page. A `<style>` the admin wrote stays inside the delivered
   * document, where it applies to the preview exactly as it will in a mail client.
   */
  interface Props {
    /** The complete mail document the server rendered. */
    html: string;
    /** Accessible name of the iframe. */
    title: string;
    testId?: string;
  }

  let { html, title, testId = 'admin-mail-preview-frame' }: Props = $props();
</script>

<iframe
  class="block h-[480px] w-full rounded-md border border-border bg-white"
  sandbox=""
  referrerpolicy="no-referrer"
  {title}
  srcdoc={html}
  data-testid={testId}
></iframe>
