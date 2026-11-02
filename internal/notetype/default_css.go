package notetype

// DefaultCSS is the style a note type created in engram starts with.
//
// It keeps the layout Anki's stock types have -- Arial, centred, black on white -- and states no
// type size. The card frame draws the face inside a body that already carries a responsive scale at
// zero specificity, and a note type rule outranks that scale, so a `font-size` here would pin every
// deck to that number and the frame's scale would never apply. A deck that wants a fixed size writes
// one in its own CSS, which is the only place the value can win.
//
// The colour pair survives the dark theme because the frame also writes the `nightMode` class on
// that body, and `body.nightMode` outranks `.card`.
const DefaultCSS = ".card {\n    font-family: arial;\n    text-align: center;\n    color: black;\n    background-color: white;\n}\n"
