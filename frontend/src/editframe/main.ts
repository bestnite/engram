import './editor.css';
import { createRuntime } from './runtime';

/**
 * Rich-text frame bootstrap. The document only contains `#editor` and this module; the parent page
 * sends the field HTML and the toolbar commands, and the runtime reports every change back.
 *
 * No application stylesheet is imported: the editing surface must stay the same whatever the SPA
 * theme does, and the frame document is served with the frame's own sandbox CSP.
 */
const runtime = createRuntime();
runtime.signalReady();
