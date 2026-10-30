import { createRuntime } from './runtime';

/**
 * Card frame bootstrap. The document only contains `<div id="qa">` and this module; everything else is driven by messages from
 * the embedding page. `ready` tells the parent that the runtime can receive `show`.
 */
const runtime = createRuntime();
runtime.signalReady();
