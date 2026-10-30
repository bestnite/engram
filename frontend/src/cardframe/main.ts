import '../app.css';
import './reviewer.css';
import './image-occlusion.css';
import { createRuntime } from './runtime';

/**
 * Card frame bootstrap. The document only contains `<div id="qa">` and this module; everything else is driven by messages from
 * the embedding page. `ready` tells the parent that the runtime can receive `show`.
 *
 * The application stylesheet is imported here as well: the frame renders card faces, and the audio, speech and answer
 * controls it attaches are built from the same utility classes and design tokens as the rest of the site.
 */
const runtime = createRuntime();
runtime.signalReady();
