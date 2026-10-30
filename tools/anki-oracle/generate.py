#!/usr/bin/env python3
"""Build the Anki reference packages and golden files used by the Go ports.

The script drives the pinned Anki Python package (`anki==26.9.3`) to build one
collection from placeholder content, renders every card, and writes:

- one `.apkg` per fixture group under `internal/notetype/testdata/anki/`;
- one golden JSON per fixture group next to it.

Nothing in the collection comes from a real deck: every field text is written
here, and every media file is generated here.

The output is byte-identical across runs. Anki assigns note, card, note type and
deck ids from the wall clock and stamps `col.mod`/`col.scm` on every save, so the
script normalises the SQLite files it produced (ids, timestamps, sync state) and
rebuilds each ZIP with a fixed entry order and a fixed entry timestamp. The
normalised collection is then reopened before rendering, so the rendered HTML
that embeds an id (`{{CardID}}`) is stable too.

Run with the interpreter from `tools/anki-oracle/.venv`; see `README.md`.
"""

from __future__ import annotations

import hashlib
import json
import shutil
import sqlite3
import struct
import sys
import tempfile
import zipfile
import zlib
from dataclasses import dataclass, field
from pathlib import Path

import zstandard
from anki.collection import (
    Collection,
    ExportAnkiPackageOptions,
    NoteIdsLimit,
)
from anki.consts import HELP_SITE
from anki.import_export_pb2 import (
    ImportAnkiPackageOptions,
    ImportAnkiPackageRequest,
    MediaEntries,
)
from anki.notetypes_pb2 import Notetype
from anki.sound import SoundOrVideoTag
from anki.utils import to_json_bytes

SOURCE = "anki==26.9.3"

REPO_ROOT = Path(__file__).resolve().parents[2]
OUT_DIR = REPO_ROOT / "internal" / "notetype" / "testdata" / "anki"

# Fixed values written into every normalised collection. The bases are arbitrary
# but must stay clear of the small ids Anki reserves for the default deck (1) and
# the default deck configuration (1), and of the note type ids handed out below.
FIXED_CRT = 1_600_000_000
FIXED_MS = 1_600_000_000_000
NOTETYPE_BASE = 1_000
DECK_BASE = 2_000
NOTE_BASE = 1_600_000_000_000
CARD_BASE = 1_500_000_000_000
PLACEHOLDER_NOTE_ID = NOTE_BASE
PLACEHOLDER_CARD_ID = CARD_BASE
FIELD_ID_BASE = 100_000
TEMPLATE_ID_BASE = 2_000_000
PLACEHOLDER_NOTETYPE_ID = NOTETYPE_BASE
PLACEHOLDER_GUID = "placeholder"
PLACEHOLDER_TEXT = (
    "Please update to the latest Anki version, then import the .colpkg/.apkg file"
)

SUBDECK_NAME = "Placeholder::Lesson 1"

# Anki's built-in "the front of this card is blank" message links to its help
# site. That host is a real domain, and a committed file may not name one, so
# rendered output has the help-site host rewritten to a reserved .invalid host.
# The value comes from the pinned package instead of being repeated here.
ANKI_DOCS_HOST = HELP_SITE.rstrip("/")
PLACEHOLDER_DOCS_HOST = "https://docs.ankiweb.invalid"

FIXTURE_CSS = ".card {\n    font-family: arial;\n    text-align: left;\n}\n"

# ---------------------------------------------------------------------------
# Fixture catalogue
# ---------------------------------------------------------------------------


@dataclass
class FixtureNote:
    guid: str
    fields: dict[str, str]
    tags: list[str] = field(default_factory=list)
    deck: str | None = None
    # Fields blanked after the note was added, so the card they generated still
    # exists while its front no longer renders any text.
    blank_after_add: list[str] = field(default_factory=list)
    # (expected, provided, combining) inputs fed to col.compare_answer(). The
    # expected text must equal the value `expected_source` derives, which the
    # generator asserts before writing the entry.
    compare_inputs: list[tuple[str, str, bool]] = field(default_factory=list)
    # Where the expected text comes from: "field:<name>" reads the named field,
    # "cloze:<name>" reads extract_cloze_for_typing() for each cloze number.
    expected_source: str = ""


@dataclass
class FixtureNotetype:
    name: str
    fields: list[str]
    templates: list[tuple[str, str, str]]
    notes: list[FixtureNote]
    kind: str = "normal"  # normal | cloze
    css: str = FIXTURE_CSS


def t(name: str, qfmt: str, afmt: str) -> tuple[str, str, str]:
    return (name, qfmt, afmt)


# The six stock note types are created by Anki itself; only their notes are
# declared here.
STOCK_NOTES: dict[str, list[FixtureNote]] = {
    "Basic": [
        FixtureNote("stock-basic-one", {"Front": "front <b>one</b>", "Back": "back one"}),
        FixtureNote("stock-basic-two", {"Front": "front two", "Back": "back two"}),
    ],
    "Basic (and reversed card)": [
        FixtureNote("stock-reversed-one", {"Front": "front one", "Back": "back one"}),
    ],
    "Basic (optional reversed card)": [
        FixtureNote(
            "stock-optional-off",
            {"Front": "front one", "Back": "back one", "Add Reverse": ""},
        ),
        FixtureNote(
            "stock-optional-on",
            {"Front": "front two", "Back": "back two", "Add Reverse": "1"},
        ),
    ],
    "Basic (type in the answer)": [
        FixtureNote(
            "stock-typing-one",
            {"Front": "front one", "Back": "42"},
            compare_inputs=[
                ("42", "42", True),
                ("42", "4", True),
                ("42", "", True),
            ],
            expected_source="field:Back",
        ),
    ],
    "Cloze": [
        FixtureNote(
            "stock-cloze-one",
            {"Text": "one {{c1::two}} three {{c2::four::a hint}}", "Back Extra": "extra"},
        ),
    ],
    "Image Occlusion": [
        FixtureNote(
            "stock-image-occlusion-one",
            {
                "Occlusion": (
                    "{{c1::image-occlusion:rect:left=0.1:top=0.2:"
                    "width=0.3:height=0.4:oi=1}}"
                ),
                "Image": '<img src="placeholder.png">',
                "Header": "header one",
                "Back Extra": "",
                "Comments": "",
            },
        ),
    ],
}


SYNTAX_NOTETYPES = [
    FixtureNotetype(
        name="Rule Field Replacement",
        fields=["Front", "Back"],
        templates=[t("Card 1", "{{Front}}", "{{FrontSide}}<hr id=answer>{{Back}}")],
        notes=[
            FixtureNote("syntax-replacement-one", {"Front": "plain field", "Back": "<b>back</b>"}),
            FixtureNote("syntax-replacement-two", {"Front": "second field", "Back": "second back"}),
        ],
    ),
    FixtureNotetype(
        name="Rule Conditional",
        fields=["Front"],
        templates=[t("Card 1", "[{{#Front}}{{Front}}{{/Front}}]", "{{FrontSide}}")],
        notes=[
            FixtureNote("syntax-conditional-text", {"Front": "content"}),
            FixtureNote("syntax-conditional-empty", {"Front": ""}),
            FixtureNote("syntax-conditional-spaces", {"Front": "   "}),
            FixtureNote("syntax-conditional-br", {"Front": "<br>"}),
            FixtureNote("syntax-conditional-div", {"Front": "<div></div>"}),
            FixtureNote("syntax-conditional-div-space", {"Front": "<div> </div>"}),
            FixtureNote("syntax-conditional-br-div", {"Front": "<br><div></div>"}),
        ],
    ),
    FixtureNotetype(
        name="Rule Inverted Conditional",
        fields=["Front"],
        templates=[t("Card 1", "[{{^Front}}empty{{/Front}}]", "{{FrontSide}}")],
        notes=[
            FixtureNote("syntax-inverted-text", {"Front": "content"}),
            FixtureNote("syntax-inverted-empty", {"Front": ""}),
            FixtureNote("syntax-inverted-spaces", {"Front": "   "}),
            FixtureNote("syntax-inverted-br", {"Front": "<br>"}),
            FixtureNote("syntax-inverted-div", {"Front": "<div></div>"}),
            FixtureNote("syntax-inverted-div-space", {"Front": "<div> </div>"}),
            FixtureNote("syntax-inverted-br-div", {"Front": "<br><div></div>"}),
        ],
    ),
    FixtureNotetype(
        name="Rule Comment",
        fields=["Front"],
        templates=[t("Card 1", "{{Front}}-before<!-- {{Front}} -->after", "{{FrontSide}}")],
        notes=[FixtureNote("syntax-comment-one", {"Front": "hidden"})],
    ),
    FixtureNotetype(
        name="Rule Delimiter Change",
        fields=["Front", "Back"],
        templates=[
            t("Card 1", "{{=<% %>=}}<%Front%>|<%Back%>", "{{=<% %>=}}<%FrontSide%>")
        ],
        notes=[FixtureNote("syntax-delimiter-one", {"Front": "left", "Back": "right"})],
    ),
    FixtureNotetype(
        name="Rule FrontSide",
        fields=["Front", "Back"],
        templates=[t("Card 1", "{{Front}}+{{Back}}", "{{FrontSide}}<hr id=answer>{{Back}}")],
        notes=[FixtureNote("syntax-frontside-one", {"Front": "front one", "Back": "back one"})],
    ),
    FixtureNotetype(
        name="Rule Special Fields",
        fields=["Front"],
        templates=[
            t(
                "Card 1",
                "{{Front}}|{{Tags}}|{{Type}}|{{Deck}}|{{Subdeck}}|{{Card}}|{{CardFlag}}",
                "{{FrontSide}}",
            )
        ],
        notes=[
            FixtureNote(
                "syntax-specials-one",
                {"Front": "front one"},
                tags=["alpha", "beta"],
                deck=SUBDECK_NAME,
            )
        ],
    ),
    FixtureNotetype(
        name="Rule Card Id",
        fields=["Front"],
        templates=[t("Card 1", "{{Front}}|{{CardID}}", "{{FrontSide}}")],
        notes=[FixtureNote("syntax-card-id-one", {"Front": "front one"})],
    ),
    FixtureNotetype(
        name="Rule Field Shadowing",
        fields=["Front", "Tags", "Deck"],
        templates=[t("Card 1", "{{Front}}|{{Tags}}|{{Deck}}", "{{FrontSide}}")],
        notes=[
            FixtureNote(
                "syntax-shadowing-one",
                {"Front": "front one", "Tags": "field-tags", "Deck": "field-deck"},
                tags=["special-tag"],
            )
        ],
    ),
    FixtureNotetype(
        name="Rule Cloze Conditional",
        fields=["Text"],
        kind="cloze",
        templates=[
            t(
                "Card 1",
                "{{cloze:Text}}{{#c1}} [c1-on]{{/c1}}{{^c1}} [c1-off]{{/c1}}",
                "{{cloze:Text}}<br>{{#c1}}[c1-on]{{/c1}}{{^c1}}[c1-off]{{/c1}}",
            )
        ],
        notes=[FixtureNote("syntax-cloze-conditional-one", {"Text": "{{c1::one}} {{c2::two}}"})],
    ),
]

FILTER_NOTETYPES = [
    FixtureNotetype(
        name="Filter Text",
        fields=["Front"],
        templates=[t("Card 1", "{{text:Front}}", "{{FrontSide}}")],
        notes=[
            FixtureNote(
                "filter-text-one",
                {"Front": '<b>bold</b> <img src="placeholder.png"> tail'},
            )
        ],
    ),
    FixtureNotetype(
        name="Filter Hint",
        fields=["Front"],
        templates=[t("Card 1", "{{hint:Front}}", "{{FrontSide}}")],
        notes=[FixtureNote("filter-hint-one", {"Front": "<b>hinted</b> text"})],
    ),
    FixtureNotetype(
        name="Filter Furigana",
        fields=["Front"],
        templates=[t("Card 1", "{{furigana:Front}}", "{{FrontSide}}")],
        notes=[FixtureNote("filter-furigana-one", {"Front": "漢字[かんじ] と カナ[かな]"})],
    ),
    FixtureNotetype(
        name="Filter Kana",
        fields=["Front"],
        templates=[t("Card 1", "{{kana:Front}}", "{{FrontSide}}")],
        notes=[FixtureNote("filter-kana-one", {"Front": "漢字[かんじ] と カナ[かな]"})],
    ),
    FixtureNotetype(
        name="Filter Kanji",
        fields=["Front"],
        templates=[t("Card 1", "{{kanji:Front}}", "{{FrontSide}}")],
        notes=[FixtureNote("filter-kanji-one", {"Front": "漢字[かんじ] と カナ[かな]"})],
    ),
    FixtureNotetype(
        name="Filter Chain",
        fields=["Front"],
        templates=[t("Card 1", "{{text:hint:Front}}", "{{FrontSide}}")],
        notes=[FixtureNote("filter-chain-one", {"Front": "<b>chained</b> text"})],
    ),
    FixtureNotetype(
        name="Filter Unknown",
        fields=["Front"],
        templates=[t("Card 1", "{{unknown:Front}}", "{{FrontSide}}")],
        notes=[FixtureNote("filter-unknown-one", {"Front": "<b>unfiltered</b> text"})],
    ),
    FixtureNotetype(
        name="Filter Empty Field Name",
        fields=["Front"],
        templates=[t("Card 1", "{{text:}}", "{{FrontSide}}")],
        notes=[FixtureNote("filter-empty-name-one", {"Front": "ignored"})],
    ),
]

CLOZE_NOTETYPES = [
    FixtureNotetype(
        name="Cloze Basic",
        fields=["Text", "Back Extra"],
        kind="cloze",
        templates=[t("Card 1", "{{cloze:Text}}", "{{cloze:Text}}<br>{{Back Extra}}")],
        notes=[
            FixtureNote(
                "cloze-basic-one",
                {"Text": "one {{c1::two}} three", "Back Extra": "extra one"},
            )
        ],
    ),
    FixtureNotetype(
        name="Cloze Hint",
        fields=["Text"],
        kind="cloze",
        templates=[t("Card 1", "{{cloze:Text}}", "{{cloze:Text}}")],
        notes=[FixtureNote("cloze-hint-one", {"Text": "before {{c1::answer::hint one}} after"})],
    ),
    FixtureNotetype(
        name="Cloze Multiple",
        fields=["Text"],
        kind="cloze",
        templates=[t("Card 1", "{{cloze:Text}}", "{{cloze:Text}}")],
        notes=[FixtureNote("cloze-multiple-one", {"Text": "{{c1,2::shared}}"})],
    ),
    FixtureNotetype(
        name="Cloze Nested",
        fields=["Text"],
        kind="cloze",
        templates=[t("Card 1", "{{cloze:Text}}", "{{cloze:Text}}")],
        notes=[FixtureNote("cloze-nested-one", {"Text": "{{c1::outer {{c2::inner}}}}"})],
    ),
    FixtureNotetype(
        name="Cloze Math",
        fields=["Text"],
        kind="cloze",
        templates=[t("Card 1", "{{cloze:Text}}", "{{cloze:Text}}")],
        notes=[
            FixtureNote(
                "cloze-math-one",
                {"Text": "{{c1::x}} \\(a}}b\\) \\[c}}d\\]"},
            )
        ],
    ),
    FixtureNotetype(
        name="Cloze Only",
        fields=["Text"],
        kind="cloze",
        templates=[
            t(
                "Card 1",
                "{{cloze:Text}}#{{cloze-only:Text}}",
                "{{cloze:Text}}#{{cloze-only:Text}}",
            )
        ],
        notes=[FixtureNote("cloze-only-one", {"Text": "one {{c1::two}} three {{c2::four}}"})],
    ),
    FixtureNotetype(
        name="Cloze Numbers",
        fields=["Text"],
        kind="cloze",
        templates=[t("Card 1", "{{cloze:Text}}", "{{cloze:Text}}")],
        notes=[
            FixtureNote(
                "cloze-numbers-one",
                {"Text": "{{c1::one}} {{c3::three}} {{c5::five}}"},
            )
        ],
    ),
    FixtureNotetype(
        name="Cloze Image Occlusion",
        fields=["Occlusion"],
        kind="cloze",
        templates=[t("Card 1", "{{cloze:Occlusion}}", "{{cloze:Occlusion}}")],
        notes=[
            FixtureNote(
                "cloze-image-occlusion-one",
                {
                    "Occlusion": (
                        "{{c1::image-occlusion:rect:left=0.1:top=0.2:"
                        "width=0.3:height=0.4:oi=1}}"
                    )
                },
            )
        ],
    ),
    FixtureNotetype(
        name="Cloze Pipe",
        fields=["Text"],
        kind="cloze",
        templates=[t("Card 1", "{{cloze:Text}}", "{{cloze:Text}}")],
        notes=[FixtureNote("cloze-pipe-one", {"Text": "{{c1::left|right}}"})],
    ),
]

CARDGEN_NOTETYPES = [
    FixtureNotetype(
        name="Cardgen Normal",
        fields=["Front", "Back"],
        templates=[
            t("Always", "{{Front}}", "{{FrontSide}}<hr id=answer>{{Back}}"),
            t("Conditional", "{{#Back}}{{Front}}{{/Back}}", "{{Back}}"),
        ],
        notes=[
            FixtureNote("cardgen-both", {"Front": "front one", "Back": "back one"}),
            FixtureNote("cardgen-no-back", {"Front": "front two", "Back": ""}),
            FixtureNote("cardgen-no-front", {"Front": "", "Back": "back three"}),
        ],
    ),
    FixtureNotetype(
        name="Cardgen Cloze",
        fields=["Text", "Extra"],
        kind="cloze",
        templates=[t("Card 1", "{{cloze:Text}}", "{{cloze:Text}}")],
        notes=[
            FixtureNote("cardgen-cloze-gap", {"Text": "{{c1::one}} {{c3::three}}", "Extra": ""}),
            FixtureNote(
                "cardgen-cloze-other-field",
                {"Text": "{{c1::one}}", "Extra": "{{c2::two}}"},
            ),
        ],
    ),
    FixtureNotetype(
        name="Cardgen Empty Existing",
        fields=["Front", "Back"],
        templates=[t("Card 1", "{{Front}}", "{{FrontSide}}<hr id=answer>{{Back}}")],
        notes=[
            FixtureNote(
                "cardgen-empty-existing-one",
                {"Front": "front one", "Back": "back one"},
                blank_after_add=["Front"],
            )
        ],
    ),
]

AV_NOTETYPES = [
    FixtureNotetype(
        name="AV Sound",
        fields=["Front", "Back"],
        templates=[t("Card 1", "{{Front}}", "{{FrontSide}}<hr id=answer>{{Back}}")],
        notes=[
            FixtureNote(
                "av-sound-one",
                {
                    "Front": 'before [sound:placeholder.wav] after [sound:placeholder2.wav]',
                    "Back": "back [sound:placeholder.wav]",
                },
            )
        ],
    ),
    FixtureNotetype(
        name="AV TTS",
        fields=["Front"],
        templates=[t("Card 1", "{{Front}}", "{{FrontSide}}")],
        notes=[
            FixtureNote(
                "av-tts-one",
                {
                    "Front": (
                        "[anki:tts lang=en_US voices=alpha,beta speed=1.25 unknown=keepme]"
                        "spoken text [sound:placeholder.wav][/anki:tts]"
                    )
                },
            )
        ],
    ),
    FixtureNotetype(
        name="AV TTS Filter",
        fields=["Front"],
        templates=[t("Card 1", "{{tts ja_JP voices=alpha speed=0.75:Front}}", "{{FrontSide}}")],
        notes=[FixtureNote("av-tts-filter-one", {"Front": "spoken <b>text</b>"})],
    ),
    FixtureNotetype(
        name="AV Replay Order",
        fields=["Front", "Back"],
        templates=[t("Card 1", "{{Front}}", "{{FrontSide}}<hr id=answer>{{Back}}")],
        notes=[
            FixtureNote(
                "av-order-one",
                {
                    "Front": (
                        "[sound:placeholder.wav][sound:placeholder2.wav] middle "
                        "[anki:tts lang=en_US]speech[/anki:tts]"
                    ),
                    "Back": "[sound:placeholder.wav] tail",
                },
            )
        ],
    ),
    FixtureNotetype(
        name="AV LaTeX",
        fields=["Front"],
        templates=[t("Card 1", "{{Front}}", "{{FrontSide}}")],
        notes=[
            FixtureNote(
                "av-latex-one",
                {"Front": "[latex]x^2[/latex] [$]y+1[/$] [$$]z=1[/$$]"},
            )
        ],
    ),
]

TYPEANSWER_NOTETYPES = [
    FixtureNotetype(
        name="Type Basic",
        fields=["Front", "Back"],
        templates=[
            t(
                "Card 1",
                "{{Front}}\n\n{{type:Back}}",
                "{{Front}}\n\n<hr id=answer>\n\n{{type:Back}}",
            )
        ],
        notes=[
            FixtureNote(
                "typeanswer-basic-one",
                {"Front": "front one", "Back": "42"},
                compare_inputs=[
                    ("42", "42", True),
                    ("42", "4", True),
                    ("42", "", True),
                    ("42", "42 ", True),
                    ("42", "42", False),
                ],
                expected_source="field:Back",
            ),
            FixtureNote(
                "typeanswer-basic-empty",
                {"Front": "front two", "Back": ""},
                compare_inputs=[
                    ("", "extra", True),
                    ("", "", True),
                ],
                expected_source="field:Back",
            )
        ],
    ),
    FixtureNotetype(
        name="Type Nc",
        fields=["Front", "Back"],
        templates=[
            t("Card 1", "{{Front}} {{type-nc:Back}}", "{{FrontSide}}<hr id=answer>{{type-nc:Back}}")
        ],
        notes=[
            FixtureNote(
                "typeanswer-nc-one",
                {"Front": "front one", "Back": "a b"},
                compare_inputs=[
                    ("a b", "ab", True),
                    ("a b", "ab", False),
                    ("a b", "a  b", False),
                ],
                expected_source="field:Back",
            )
        ],
    ),
    FixtureNotetype(
        name="Type Cloze",
        fields=["Text"],
        kind="cloze",
        templates=[
            t(
                "Card 1",
                "{{cloze:Text}} {{type:cloze:Text}}",
                "{{cloze:Text}}<hr id=answer>{{type:cloze:Text}}",
            )
        ],
        notes=[
            FixtureNote(
                "typeanswer-cloze-one",
                {"Text": "one {{c1::two}} three {{c2::four::a hint}}"},
                compare_inputs=[
                    ("two", "two", True),
                    ("two", "too", True),
                    ("two", "two", False),
                    ("four", "four", True),
                ],
                expected_source="cloze:Text",
            )
        ],
    ),
]


@dataclass
class Package:
    fixture: str  # file-name stem
    notetypes: list[FixtureNotetype]
    formats: tuple[str, ...]
    stock: bool = False


PACKAGES = [
    Package(
        fixture="stock",
        notetypes=[],
        formats=("anki2", "anki21", "anki21b"),
        stock=True,
    ),
    Package(
        fixture="template-syntax",
        notetypes=SYNTAX_NOTETYPES,
        formats=("anki21b",),
    ),
    Package(
        fixture="template-filters",
        notetypes=FILTER_NOTETYPES,
        formats=("anki21b",),
    ),
    Package(
        fixture="cloze",
        notetypes=CLOZE_NOTETYPES,
        formats=("anki21b",),
    ),
    Package(
        fixture="cardgen",
        notetypes=CARDGEN_NOTETYPES,
        formats=("anki21b",),
    ),
    Package(
        fixture="audio-tts-latex",
        notetypes=AV_NOTETYPES,
        formats=("anki21b",),
    ),
    Package(
        fixture="typeanswer",
        notetypes=TYPEANSWER_NOTETYPES,
        formats=("anki21b",),
    ),
]


# rule id -> (requirement in the port, fixture note type, what the golden shows)
RULE_ROWS: list[tuple[str, str, str, str]] = [
    # M17-7 syntax
    ("syntax.field-replacement", "`{{Field}}` inserts the field HTML",
     "template-syntax: Rule Field Replacement", "syntax-replacement-one/two"),
    ("syntax.conditional", "`{{#F}}…{{/F}}` keeps the body when F is non-empty",
     "template-syntax: Rule Conditional", "syntax-conditional-text"),
    ("syntax.empty-field", "a field holding only whitespace, `<br>` or `<div>` is empty",
     "template-syntax: Rule Conditional / Rule Inverted Conditional",
     "syntax-conditional-* / syntax-inverted-*"),
    ("syntax.inverted-conditional", "`{{^F}}…{{/F}}` keeps the body when F is empty",
     "template-syntax: Rule Inverted Conditional", "syntax-inverted-*"),
    ("syntax.comment", "`{{…}}` inside `<!-- -->` stays literal",
     "template-syntax: Rule Comment", "syntax-comment-one"),
    ("syntax.delimiter-change", "`{{=<% %>=}}` switches the delimiters to `<% %>`",
     "template-syntax: Rule Delimiter Change", "syntax-delimiter-one"),
    ("syntax.front-side", "`{{FrontSide}}` is the rendered question",
     "template-syntax: Rule FrontSide", "syntax-frontside-one"),
    ("syntax.tags", "`{{Tags}}` is the space-joined tag list",
     "template-syntax: Rule Special Fields", "syntax-specials-one"),
    ("syntax.type", "`{{Type}}` is the note type name",
     "template-syntax: Rule Special Fields", "syntax-specials-one"),
    ("syntax.deck", "`{{Deck}}` is the full deck path",
     "template-syntax: Rule Special Fields", "syntax-specials-one"),
    ("syntax.subdeck", "`{{Subdeck}}` is the deck name without its parents",
     "template-syntax: Rule Special Fields", "syntax-specials-one"),
    ("syntax.card", "`{{Card}}` is the template name",
     "template-syntax: Rule Special Fields", "syntax-specials-one"),
    ("syntax.card-flag", "`{{CardFlag}}` renders `flag0` for an unflagged card",
     "template-syntax: Rule Special Fields", "syntax-specials-one"),
    ("syntax.card-id", "`{{CardID}}` renders the card id",
     "template-syntax: Rule Card Id", "syntax-card-id-one"),
    ("syntax.field-shadowing", "a note field shadows the special field of the same name",
     "template-syntax: Rule Field Shadowing", "syntax-shadowing-one"),
    ("syntax.cloze-conditional", "`{{#c1}}` is true only on the card for cloze 1",
     "template-syntax: Rule Cloze Conditional", "syntax-cloze-conditional-one"),
    # M17-7 filters
    ("filter.text", "`text` strips HTML",
     "template-filters: Filter Text", "filter-text-one"),
    ("filter.hint", "`hint` renders Anki's collapsible hint markup",
     "template-filters: Filter Hint", "filter-hint-one"),
    ("filter.furigana", "`furigana` renders `base[reading]` as ruby",
     "template-filters: Filter Furigana", "filter-furigana-one"),
    ("filter.kana", "`kana` keeps only the reading",
     "template-filters: Filter Kana", "filter-kana-one"),
    ("filter.kanji", "`kanji` keeps only the base text",
     "template-filters: Filter Kanji", "filter-kanji-one"),
    ("filter.cloze", "`cloze` renders the cloze markup for the card",
     "cloze: Cloze Basic", "cloze-basic-one"),
    ("filter.cloze-only", "`cloze-only` renders only the cloze deletions",
     "cloze: Cloze Only", "cloze-only-one"),
    ("filter.type", "`type` renders the `[[type:Field]]` placeholder",
     "typeanswer: Type Basic", "typeanswer-basic-one"),
    ("filter.type-cloze", "`type:cloze` renders the `[[type:cloze:Field]]` placeholder",
     "typeanswer: Type Cloze", "typeanswer-cloze-one"),
    ("filter.type-nc", "`type-nc` renders the `[[type:nc:Field]]` placeholder",
     "typeanswer: Type Nc", "typeanswer-nc-one"),
    ("filter.chain", "a filter chain applies left to right",
     "template-filters: Filter Chain", "filter-chain-one"),
    ("filter.unknown", "an unknown filter is skipped without error",
     "template-filters: Filter Unknown", "filter-unknown-one"),
    ("filter.empty-field-name", "an empty field name with a filter passes an empty string",
     "template-filters: Filter Empty Field Name", "filter-empty-name-one"),
    ("filter.tts", "`tts <lang> <options>` replaces the field with a TTS tag",
     "audio-tts-latex: AV TTS Filter", "av-tts-filter-one"),
    # M17-8
    ("cloze.basic", "`{{cN::text}}`", "cloze: Cloze Basic", "cloze-basic-one"),
    ("cloze.hint", "`{{cN::text::hint}}`", "cloze: Cloze Hint", "cloze-hint-one"),
    ("cloze.multiple", "`{{c1,2::…}}`", "cloze: Cloze Multiple", "cloze-multiple-one"),
    ("cloze.nested", "a nested cloze also generates its own card",
     "cloze: Cloze Nested", "cloze-nested-one"),
    ("cloze.math", "`}}` inside `\\(…\\)` or `\\[…\\]` does not close a cloze",
     "cloze: Cloze Math", "cloze-math-one"),
    ("cloze.only", "`cloze-only` output", "cloze: Cloze Only", "cloze-only-one"),
    ("cloze.numbers", "the cloze numbers present in the fields",
     "cloze: Cloze Numbers", "cloze-numbers-one"),
    ("cloze.image-occlusion", "`{{cN::image-occlusion:<shape>:…}}` markup",
     "cloze: Cloze Image Occlusion", "cloze-image-occlusion-one"),
    ("cloze.pipe", "`|` is ordinary text", "cloze: Cloze Pipe", "cloze-pipe-one"),
    # M17-9
    ("cardgen.normal", "a template generates a card when its front renders non-empty",
     "cardgen: Cardgen Normal", "cardgen-both/no-back/no-front"),
    ("cardgen.cloze", "a cloze note generates one card per cloze number in its fields",
     "cardgen: Cardgen Cloze", "cardgen-cloze-gap/other-field"),
    ("cardgen.empty-existing", "an existing card whose front is now empty is reported, not deleted",
     "cardgen: Cardgen Empty Existing", "cardgen-empty-existing-one"),
    # M17-10
    ("av.sound", "`[sound:<name>]` is replaced by a play marker and listed as a tag",
     "audio-tts-latex: AV Sound", "av-sound-one"),
    ("av.tts", "`[anki:tts …]…[/anki:tts]` keeps unknown options",
     "audio-tts-latex: AV TTS", "av-tts-one"),
    ("av.replay-order", "tags are listed and marked in document order",
     "audio-tts-latex: AV Replay Order", "av-order-one"),
    ("av.latex", "`[latex]` becomes an `<img class=latex>` with a `latex-<sha1>.png` name",
     "audio-tts-latex: AV LaTeX", "av-latex-one"),
    ("av.latex-inline", "`[$]…[/$]` is the inline-maths form",
     "audio-tts-latex: AV LaTeX", "av-latex-one"),
    ("av.latex-display", "`[$$]…[/$$]` is the display-maths form",
     "audio-tts-latex: AV LaTeX", "av-latex-one"),
    # M17-11
    ("typeanswer.good", "`compare_answer` marks a matching answer `typeGood`",
     "typeanswer: Type Basic", "typeanswer-basic-one"),
    ("typeanswer.bad", "`compare_answer` marks a wrong answer `typeBad`",
     "typeanswer: Type Basic", "typeanswer-basic-one"),
    ("typeanswer.missed", "`compare_answer` marks a missing character `typeMissed`",
     "typeanswer: Type Basic", "typeanswer-basic-one"),
    ("typeanswer.combining", "whitespace is combined before comparing",
     "typeanswer: Type Nc", "typeanswer-nc-one"),
    ("typeanswer.nc", "`type-nc` compares without combining",
     "typeanswer: Type Nc", "typeanswer-nc-one"),
    ("typeanswer.cloze-extraction", "the expected text for `type:cloze`",
     "typeanswer: Type Cloze", "typeanswer-cloze-one"),
    # stock
    ("stock.notetypes", "the six stock note types",
     "stock", "stock-*"),
    ("stock.formats", "the same stock content in the three package formats",
     "stock.anki2 / stock.anki21 / stock.anki21b", "every stock note"),
]


# ---------------------------------------------------------------------------
# Generated media (fixed bytes, no binary blobs in the repository)
# ---------------------------------------------------------------------------


def _png_chunk(kind: bytes, payload: bytes) -> bytes:
    return (
        struct.pack(">I", len(payload))
        + kind
        + payload
        + struct.pack(">I", zlib.crc32(kind + payload) & 0xFFFFFFFF)
    )


def make_png(r: int, g: int, b: int) -> bytes:
    ihdr = struct.pack(">IIBBBBB", 1, 1, 8, 2, 0, 0, 0)
    idat = zlib.compress(bytes([0, r, g, b]), 9)
    return (
        b"\x89PNG\r\n\x1a\n"
        + _png_chunk(b"IHDR", ihdr)
        + _png_chunk(b"IDAT", idat)
        + _png_chunk(b"IEND", b"")
    )


def make_wav(sample: int) -> bytes:
    frames = struct.pack("<8h", *([sample, -sample] * 4))
    header = struct.pack(
        "<4sI4s4sIHHIIHH4sI",
        b"RIFF",
        36 + len(frames),
        b"WAVE",
        b"fmt ",
        16,
        1,
        1,
        8000,
        16000,
        2,
        16,
        b"data",
        len(frames),
    )
    return header + frames


def media_bytes() -> dict[str, bytes]:
    return {
        "placeholder.png": make_png(0x20, 0x40, 0x60),
        "placeholder2.png": make_png(0x60, 0x40, 0x20),
        "placeholder.wav": make_wav(0),
        "placeholder2.wav": make_wav(64),
    }


# ---------------------------------------------------------------------------
# Collection build
# ---------------------------------------------------------------------------


def build_notetype(col: Collection, spec: FixtureNotetype) -> None:
    nt = col.models.new(spec.name)
    nt["flds"] = []
    nt["tmpls"] = []
    for name in spec.fields:
        col.models.add_field(nt, col.models.new_field(name))
    for tname, qfmt, afmt in spec.templates:
        template = col.models.new_template(tname or "Card 1")
        template["qfmt"] = qfmt
        template["afmt"] = afmt
        col.models.add_template(nt, template)
    nt["css"] = spec.css
    if spec.kind == "cloze":
        nt["type"] = 1
    col.models.add_dict(nt)


def stabilise_notetype_ids(col: Collection) -> None:
    """Give every field and template a fixed id.

    Anki 26.09 assigns field and template ids from a random source, and stores
    them in the note type data that both the modern schema and the legacy export
    carry. Without this the databases differ between runs even after note and
    card ids are normalised.
    """
    for index, notetype in enumerate(sorted(col.models.all(), key=lambda nt: nt["name"])):
        for ordinal, field_config in enumerate(notetype["flds"]):
            field_config["id"] = FIELD_ID_BASE + index * 100 + ordinal
        for ordinal, template in enumerate(notetype["tmpls"]):
            template["id"] = TEMPLATE_ID_BASE + index * 100 + ordinal
        col.models.update_dict(notetype)


def build_collection(path: Path) -> None:
    col = Collection(str(path))
    for name, data in media_bytes().items():
        col.media.write_data(name, data)

    deck_ids = {"Default": 1}
    deck_ids[SUBDECK_NAME] = col.decks.add_normal_deck_with_name(SUBDECK_NAME).id

    for spec in [nt for pkg in PACKAGES[1:] for nt in pkg.notetypes]:
        build_notetype(col, spec)

    stabilise_notetype_ids(col)

    for name, notes in STOCK_NOTES.items():
        notetype = col.models.by_name(name)
        assert notetype is not None, name
        for spec in notes:
            note = col.new_note(notetype)
            for field_name, value in spec.fields.items():
                note[field_name] = value
            note.tags = spec.tags
            note.guid = spec.guid
            col.add_note(note, deck_ids[spec.deck or "Default"])

    for spec in [nt for pkg in PACKAGES[1:] for nt in pkg.notetypes]:
        notetype = col.models.by_name(spec.name)
        assert notetype is not None, spec.name
        for note_spec in spec.notes:
            note = col.new_note(notetype)
            for field_name, value in note_spec.fields.items():
                note[field_name] = value
            note.tags = note_spec.tags
            note.guid = note_spec.guid
            col.add_note(note, deck_ids[note_spec.deck or "Default"])
            if note_spec.blank_after_add:
                # Re-read and blank the field: Anki keeps the already generated
                # card and reports it as an empty card instead of deleting it.
                stored = col.get_note(note.id)
                for field_name in note_spec.blank_after_add:
                    stored[field_name] = ""
                col.update_note(stored)

    col.close()


# ---------------------------------------------------------------------------
# SQLite normalisation
# ---------------------------------------------------------------------------


def unicase(left: str, right: str) -> int:
    """The collation Anki's schema declares for note type and field names."""
    left_folded = left.lower()
    right_folded = right.lower()
    if left_folded < right_folded:
        return -1
    if left_folded > right_folded:
        return 1
    return 0


def connect(path: Path) -> sqlite3.Connection:
    conn = sqlite3.connect(str(path))
    conn.create_collation("unicase", unicase)
    return conn


def field_checksum(text: str) -> int:
    return int.from_bytes(hashlib.sha1(text.encode()).digest()[:4], "big")


def remap_value(value, id_map: dict[int, int]):
    if isinstance(value, dict):
        return {key: remap_value(item, id_map) for key, item in value.items()}
    if isinstance(value, list):
        return [remap_value(item, id_map) for item in value]
    if isinstance(value, bool):
        return value
    if isinstance(value, int):
        return id_map.get(value, value)
    return value


def remap_keys(value, id_map: dict[int, int]):
    """Remap a JSON object whose keys are ids."""
    result = {}
    for key, item in value.items():
        number = int(key)
        result[str(id_map.get(number, number))] = remap_value(item, id_map)
    return result


def normalise_conf(
    conf: dict,
    deck_map: dict[int, int],
    nt_map: dict[int, int],
    valid_notetypes: set[int],
) -> dict:
    """Rewrite the collection config to fixed, self-consistent values.

    Anki rewrites `curModel` with a fresh wall-clock id during export, and keeps
    per-deck/per-note-type "last used" hints keyed by id. Both vary per run and
    neither affects rendering, so the hints are dropped and the two pointers are
    pinned to a value that exists in the package.
    """
    result = {}
    for key, value in conf.items():
        if key.startswith("_deck_") or key.startswith("_nt_"):
            continue
        if key in ("curDeck", "lastDeck"):
            result[key] = deck_map.get(value, 1) if isinstance(value, int) else 1
        elif key == "curModel":
            mapped = nt_map.get(value, value) if isinstance(value, int) else value
            result[key] = mapped if mapped in valid_notetypes else min(valid_notetypes)
        elif key == "activeDecks" and isinstance(value, list):
            decks = [deck_map.get(item, item) for item in value]
            decks = [item for item in decks if item in deck_map.values()] or [1]
            result[key] = decks
        else:
            result[key] = remap_value(value, {**deck_map, **nt_map})
    return result


def normalise_tables(conn: sqlite3.Connection) -> None:
    tables = {
        row[0]
        for row in conn.execute("select name from sqlite_master where type='table'")
    }

    if "notetypes" in tables:
        rows = conn.execute("select id, name from notetypes order by name").fetchall()
        nt_map = {row[0]: NOTETYPE_BASE + index for index, row in enumerate(rows)}
        models_blob = None
    else:
        models_blob = conn.execute("select models from col").fetchone()[0]
        models = json.loads(models_blob or "{}")
        pairs = sorted(((int(key), value["name"]) for key, value in models.items()),
                       key=lambda pair: pair[1])
        nt_map = {pair[0]: NOTETYPE_BASE + index for index, pair in enumerate(pairs)}

    if "decks" in tables:
        rows = conn.execute("select id, name from decks order by name").fetchall()
        deck_map = {}
        next_deck_id = DECK_BASE
        for deck_id, name in rows:
            if name == "Default":
                deck_map[deck_id] = 1
            else:
                deck_map[deck_id] = next_deck_id
                next_deck_id += 1
        decks_blob = None
    else:
        decks_blob = conn.execute("select decks from col").fetchone()[0]
        decks = json.loads(decks_blob or "{}")
        pairs = sorted(((int(key), value["name"]) for key, value in decks.items()),
                       key=lambda pair: pair[1])
        deck_map = {}
        next_deck_id = DECK_BASE
        for deck_id, name in pairs:
            if name == "Default":
                deck_map[deck_id] = 1
            else:
                deck_map[deck_id] = next_deck_id
                next_deck_id += 1

    # A package export can leave the field and template rows of note types it
    # dropped behind: they are unreachable and their ids are still wall-clock
    # values, so remove them before the ids are rewritten.
    if "fields" in tables and "notetypes" in tables:
        conn.execute("delete from fields where ntid not in (select id from notetypes)")
    if "templates" in tables and "notetypes" in tables:
        conn.execute("delete from templates where ntid not in (select id from notetypes)")

    note_rows = conn.execute("select id, guid from notes order by guid").fetchall()
    note_map = {row[0]: NOTE_BASE + index for index, row in enumerate(note_rows)}

    card_rows = conn.execute("select id, nid, ord from cards").fetchall()
    card_rows.sort(key=lambda row: (note_map[row[1]], row[2]))
    card_map = {row[0]: CARD_BASE + index for index, row in enumerate(card_rows)}

    if "notetypes" in tables:
        for old, new in nt_map.items():
            if old != new:
                conn.execute("update notetypes set id = ? where id = ?", (new, old))
    if "decks" in tables:
        for old, new in deck_map.items():
            if old != new:
                conn.execute("update decks set id = ? where id = ?", (new, old))
    for old, new in note_map.items():
        if old != new:
            conn.execute("update notes set id = ? where id = ?", (new, old))
    for old, new in card_map.items():
        if old != new:
            conn.execute("update cards set id = ? where id = ?", (new, old))

    if "fields" in tables:
        for old, new in nt_map.items():
            conn.execute("update fields set ntid = ? where ntid = ?", (new, old))
    if "templates" in tables:
        for old, new in nt_map.items():
            conn.execute("update templates set ntid = ? where ntid = ?", (new, old))

    for old, new in nt_map.items():
        conn.execute("update notes set mid = ? where mid = ?", (new, old))
    for old, new in note_map.items():
        conn.execute("update cards set nid = ? where nid = ?", (new, old))
    for old, new in deck_map.items():
        conn.execute("update cards set did = ? where did = ?", (new, old))
        conn.execute("update cards set odid = ? where odid = ?", (new, old))

    conn.execute(
        "update notes set mod = ?, usn = ?", (FIXED_MS, -1)
    )
    for index, row in enumerate(card_rows):
        conn.execute(
            "update cards set mod = ?, usn = ?, due = ? where id = ?",
            (FIXED_MS, -1, index + 1, card_map[row[0]]),
        )
    conn.execute(
        "update col set crt = ?, mod = ?, scm = ?, dty = 0, usn = 0, ls = 0",
        (FIXED_CRT, FIXED_MS, FIXED_MS),
    )

    if "notetypes" in tables:
        conn.execute("update notetypes set mtime_secs = 0, usn = 0")
    if "decks" in tables:
        conn.execute("update decks set mtime_secs = 0, usn = 0")
    if "deck_config" in tables:
        conn.execute("update deck_config set mtime_secs = 0, usn = 0")
    valid_notetypes = set(nt_map.values())
    if "config" in tables:
        config = {}
        for key, value in conn.execute("select key, val from config").fetchall():
            try:
                config[key] = json.loads(value)
            except (TypeError, ValueError):
                continue
        config = normalise_conf(config, deck_map, nt_map, valid_notetypes)
        conn.execute("delete from config")
        for key, value in config.items():
            conn.execute(
                "insert into config (key, usn, mtime_secs, val) values (?, 0, 0, ?)",
                (key, json.dumps(value, sort_keys=True)),
            )
    if "tags" in tables:
        conn.execute("update tags set usn = 0")
    if "graves" in tables:
        conn.execute("delete from graves")
    if "revlog" in tables:
        conn.execute("delete from revlog")

    if models_blob is not None:
        models = json.loads(models_blob or "{}")
        models = remap_keys(models, nt_map)
        for key, value in models.items():
            value["id"] = int(key)
            if "did" in value:
                value["did"] = deck_map.get(value["did"], value["did"])
        conn.execute(
            "update col set models = ?", (json.dumps(models, sort_keys=True),)
        )
        decks = json.loads(decks_blob or "{}")
        decks = remap_keys(decks, deck_map)
        for key, value in decks.items():
            value["id"] = int(key)
        conn.execute("update col set decks = ?", (json.dumps(decks, sort_keys=True),))
        conf = json.loads(conn.execute("select conf from col").fetchone()[0] or "{}")
        conf = normalise_conf(conf, deck_map, nt_map, valid_notetypes)
        conf["nextPos"] = len(card_rows) + 1
        conn.execute(
            "update col set conf = ?", (json.dumps(conf, sort_keys=True),)
        )

    for table in ("sqlite_stat1", "sqlite_stat4"):
        conn.execute("drop table if exists %s" % table)

    conn.commit()


def normalise_collection(path: Path) -> None:
    conn = connect(path)
    normalise_tables(conn)
    conn.execute("vacuum")
    conn.commit()
    conn.close()


def normalise_placeholder(path: Path) -> None:
    """Reduce the legacy fallback database to a fixed, minimal collection.

    Anki copies every note type of the source collection into this file, so two
    packages of the same run would differ and two runs would differ in ids. The
    file exists only to tell an old client to update, so it carries one stock
    note type, one deck and one note with fixed values.
    """
    conn = connect(path)
    tables = {
        row[0]
        for row in conn.execute("select name from sqlite_master where type='table'")
    }
    assert "notetypes" not in tables, "placeholder must use the legacy schema"

    models = json.loads(conn.execute("select models from col").fetchone()[0] or "{}")
    # Prefer the stock Basic note type so the fallback looks like the real one;
    # an export of a collection without it still yields a valid placeholder.
    by_name = sorted(models.values(), key=lambda value: value["name"])
    basic = next((value for value in by_name if value["name"] == "Basic"), by_name[0])
    basic = json.loads(json.dumps(basic))
    basic["id"] = PLACEHOLDER_NOTETYPE_ID
    basic["did"] = 1
    for ordinal, field_config in enumerate(basic["flds"]):
        field_config["id"] = FIELD_ID_BASE + ordinal
    for ordinal, template in enumerate(basic["tmpls"]):
        template["id"] = TEMPLATE_ID_BASE + ordinal

    decks = json.loads(conn.execute("select decks from col").fetchone()[0] or "{}")
    default = next(value for value in decks.values() if value["name"] == "Default")
    default = json.loads(json.dumps(default))
    default["id"] = 1

    conf = json.loads(conn.execute("select conf from col").fetchone()[0] or "{}")
    conf = {
        key: value
        for key, value in conf.items()
        if not key.startswith("_deck_")
        and not key.startswith("_nt_")
        and key not in ("curModel", "curDeck", "lastDeck")
    }
    conf.update({"activeDecks": [1], "curDeck": 1, "curModel": PLACEHOLDER_NOTETYPE_ID,
                 "nextPos": 2})

    dconf = json.loads(conn.execute("select dconf from col").fetchone()[0] or "{}")
    # The placeholder deck references deck configuration 1; keep only that entry,
    # or the first one if the export named it differently.
    kept = "1" if "1" in dconf else sorted(dconf)[0]
    dconf = {kept: dconf[kept]}

    conn.execute(
        "update col set crt = ?, mod = ?, scm = ?, dty = 0, usn = 0, ls = 0,"
        " conf = ?, models = ?, decks = ?, dconf = ?, tags = '{}'",
        (
            FIXED_CRT,
            FIXED_MS,
            FIXED_MS,
            json.dumps(conf, sort_keys=True),
            json.dumps({str(PLACEHOLDER_NOTETYPE_ID): basic}, sort_keys=True),
            json.dumps({"1": default}, sort_keys=True),
            json.dumps(dconf, sort_keys=True),
        ),
    )
    conn.execute("delete from notes")
    conn.execute(
        "insert into notes (id, guid, mid, mod, usn, tags, flds, sfld, csum, flags, data)"
        " values (?, ?, ?, ?, ?, '', ?, ?, ?, 0, '')",
        (
            PLACEHOLDER_NOTE_ID,
            PLACEHOLDER_GUID,
            PLACEHOLDER_NOTETYPE_ID,
            FIXED_MS,
            -1,
            PLACEHOLDER_TEXT,
            PLACEHOLDER_TEXT,
            field_checksum(PLACEHOLDER_TEXT),
        ),
    )
    conn.execute("delete from cards")
    conn.execute(
        "insert into cards (id, nid, did, ord, mod, usn, type, queue, due, ivl, factor,"
        " reps, lapses, left, odue, odid, flags, data)"
        " values (?, ?, 1, 0, ?, -1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, '{}')",
        (PLACEHOLDER_CARD_ID, PLACEHOLDER_NOTE_ID, FIXED_MS),
    )
    conn.execute("delete from graves")
    conn.execute("delete from revlog")
    for table in ("sqlite_stat1", "sqlite_stat4"):
        conn.execute("drop table if exists %s" % table)
    conn.commit()
    conn.execute("vacuum")
    conn.commit()
    conn.close()


# ---------------------------------------------------------------------------
# Package post-processing
# ---------------------------------------------------------------------------


ZIP_TIMESTAMP = (1980, 1, 1, 0, 0, 0)


def zstd_compress(data: bytes) -> bytes:
    return zstandard.ZstdCompressor(
        level=19, write_checksum=False, write_content_size=True, write_dict_id=False
    ).compress(data)


def zstd_decompress(data: bytes) -> bytes:
    # Anki writes frames without a content size, so the streaming interface is
    # required: decompress() alone rejects such a frame.
    return zstandard.ZstdDecompressor().decompressobj().decompress(data)


def normalise_media(entries: dict[str, bytes], fmt: str) -> None:
    """Order the media files by name and renumber them from zero.

    Anki numbers media in hash-map order, so the same two files land under
    different zip indexes on different runs. The mapping and the entries are
    rebuilt from the sorted names.
    """
    numbered = sorted((name for name in entries if name.isdigit()), key=int)
    if not numbered:
        return

    if fmt == "anki21b":
        proto = MediaEntries()
        proto.ParseFromString(zstd_decompress(entries["media"]))
        names = [entry.name for entry in proto.entries]
        payloads = {names[index]: entries[str(index)] for index in range(len(names))}
        ordered = sorted(names)
        rebuilt = MediaEntries()
        for index, name in enumerate(ordered):
            source = next(entry for entry in proto.entries if entry.name == name)
            entry = rebuilt.entries.add()
            entry.CopyFrom(source)
            entry.legacy_zip_filename = index
        entries["media"] = zstd_compress(rebuilt.SerializeToString())
    else:
        mapping = json.loads(entries["media"])
        payloads = {mapping[str(index)]: entries[str(index)] for index in range(len(mapping))}
        ordered = sorted(payloads)
        entries["media"] = json.dumps(
            {str(index): name for index, name in enumerate(ordered)}, separators=(",", ":")
        )

    for index, name in enumerate(ordered):
        entries[str(index)] = payloads[name]


def rewrite_package(src: Path, dst: Path, *, fmt: str, workdir: Path) -> None:
    """Rewrite an exported package with normalised databases and a fixed ZIP."""
    with zipfile.ZipFile(src) as archive:
        entries = {info.filename: archive.read(info.filename) for info in archive.infolist()}

    normalise_media(entries, fmt)

    database_path = workdir / "package.db"
    if fmt == "anki2":
        # The oldest format: one schema-11 database named collection.anki2 and a
        # JSON media map, with no meta entry. The legacy export puts the real
        # database in collection.anki21 and a fallback in collection.anki2.
        database_path.write_bytes(entries["collection.anki21"])
        normalise_collection(database_path)
        written = {
            "collection.anki2": database_path.read_bytes(),
            "media": entries["media"],
        }
        order = ["collection.anki2", "media"]
    else:
        database_name = "collection.anki21" if fmt == "anki21" else "collection.anki21b"
        payload = entries[database_name]
        if fmt == "anki21b":
            payload = zstd_decompress(payload)
        database_path.write_bytes(payload)
        normalise_collection(database_path)
        payload = database_path.read_bytes()
        written = dict(entries)
        written[database_name] = payload if fmt == "anki21" else zstd_compress(payload)

        placeholder_path = workdir / "placeholder.db"
        placeholder_path.write_bytes(entries["collection.anki2"])
        normalise_placeholder(placeholder_path)
        written["collection.anki2"] = placeholder_path.read_bytes()
        order = ["meta", database_name, "collection.anki2", "media"]

    media_names = sorted((name for name in entries if name.isdigit()), key=int)
    for name in media_names:
        written[name] = entries[name]
    order += media_names

    assert set(order) == set(written), (sorted(order), sorted(written))

    # Fixed entry order and fixed timestamps: the ZIP header holds the only
    # remaining run-to-run difference after the databases are normalised.
    with zipfile.ZipFile(dst, "w") as archive:
        for name in order:
            info = zipfile.ZipInfo(name, date_time=ZIP_TIMESTAMP)
            info.compress_type = (
                zipfile.ZIP_DEFLATED if name == "collection.anki21" else zipfile.ZIP_STORED
            )
            info.external_attr = 0o600 << 16
            if info.compress_type == zipfile.ZIP_DEFLATED:
                archive.writestr(info, written[name], compresslevel=9)
            else:
                archive.writestr(info, written[name])


# ---------------------------------------------------------------------------
# Rendering and golden output
# ---------------------------------------------------------------------------


def av_tag_syntax(tag) -> str:
    if isinstance(tag, SoundOrVideoTag):
        return "[sound:%s]" % tag.filename
    options = ["lang=%s" % tag.lang]
    if tag.voices:
        options.append("voices=%s" % ",".join(tag.voices))
    options.append("speed=%s" % tag.speed)
    options.extend(tag.other_args)
    return "[anki:tts %s]%s[/anki:tts]" % (" ".join(options), tag.field_text)


def av_tag_struct(tag) -> dict:
    if isinstance(tag, SoundOrVideoTag):
        return {"kind": "sound", "name": tag.filename}
    return {
        "kind": "tts",
        "field_text": tag.field_text,
        "lang": tag.lang,
        "voices": list(tag.voices),
        "speed": tag.speed,
        "other_args": list(tag.other_args),
    }


def nodes_text(nodes) -> str:
    parts = []
    for node in nodes:
        kind = node.WhichOneof("value")
        assert kind == "text", "unrendered replacement: %r" % (kind,)
        parts.append(node.text)
    return "".join(parts)


def play_markers(text: str, side: str) -> list[int]:
    marker = "[anki:play:%s:" % side
    found = []
    start = 0
    while True:
        index = text.find(marker, start)
        if index < 0:
            return found
        end = text.index("]", index)
        found.append(int(text[index + len(marker) : end]))
        start = end + 1


def expected_card_ordinals(col: Collection, note) -> list[int]:
    """The card set Anki generates from the note content.

    A normal note type yields one card per template whose front renders
    non-empty; a cloze note type yields one card per cloze number found in the
    note's fields, with the card ordinal equal to the number minus one. Both
    are computed with Anki's own renderer and cloze-number scan, so the golden
    is not a restatement of this script's assumptions.
    """
    notetype = note.note_type()
    if notetype["type"] == 1:
        return sorted(number - 1 for number in note.cloze_numbers_in_fields())
    ordinals = []
    for template in notetype["tmpls"]:
        rendered = col._backend.render_uncommitted_card_legacy(
            note=note._to_backend_note(),
            card_ord=template["ord"],
            template=to_json_bytes(template),
            fill_empty=False,
            partial_render=False,
        )
        if not rendered.is_empty:
            ordinals.append(template["ord"])
    return ordinals


def render_note(col: Collection, note, notetype_name: str, spec: FixtureNote) -> dict:
    cards = []
    for cid in col.card_ids_of_note(note.id):
        card = col.get_card(cid)
        partial = col._backend.render_existing_card(
            card_id=cid, browser=False, partial_render=False
        )
        output = card.render_output()
        q_markers = play_markers(output.question_text, "q")
        a_markers = play_markers(output.answer_text, "a")
        assert q_markers == list(range(len(output.question_av_tags))), (
            notetype_name,
            note.guid,
            q_markers,
            output.question_av_tags,
        )
        assert a_markers == list(range(len(output.answer_av_tags))), (
            notetype_name,
            note.guid,
            a_markers,
            output.answer_av_tags,
        )
        cards.append(
            {
                "id": card.id,
                "ordinal": card.ord,
                "front_is_empty": partial.is_empty,
                "question_html": replace_real_domain(card.question()),
                "answer_html": replace_real_domain(card.answer()),
                "question_css": output.css,
                "question_nodes_text": replace_real_domain(nodes_text(partial.question_nodes)),
                "answer_nodes_text": replace_real_domain(nodes_text(partial.answer_nodes)),
                "question_av_tags": [av_tag_syntax(tag) for tag in output.question_av_tags],
                "answer_av_tags": [av_tag_syntax(tag) for tag in output.answer_av_tags],
                "question_av_tags_parsed": [
                    av_tag_struct(tag) for tag in output.question_av_tags
                ],
                "answer_av_tags_parsed": [
                    av_tag_struct(tag) for tag in output.answer_av_tags
                ],
            }
        )
    cards.sort(key=lambda entry: entry["ordinal"])

    expected = expected_card_ordinals(col, note)
    observed = [entry["ordinal"] for entry in cards]
    if observed != expected:
        if spec.blank_after_add:
            # The note was full when Anki generated its cards; blanking a field
            # left them in place and their fronts no longer render.
            assert all(entry["front_is_empty"] for entry in cards), note.guid
        else:
            # Anki's add-note path still creates the card for template 0 when
            # no template front renders any text; that card is empty.
            assert observed == [0] and expected == [], (note.guid, observed, expected)
            assert cards[0]["front_is_empty"], (note.guid, "guarantee card is not empty")

    entry = {
        "guid": note.guid,
        "notetype_name": notetype_name,
        "fields": {
            field["name"]: note[field["name"]] for field in note.note_type()["flds"]
        },
        "cardgen_expected_ordinals": expected,
        "cards": cards,
    }
    if note.note_type()["type"] == 1:
        entry["cloze_numbers"] = sorted(note.cloze_numbers_in_fields())
    if spec.expected_source.startswith("cloze:"):
        field_name = spec.expected_source.split(":", 1)[1]
        entry["cloze_typing"] = [
            {
                "ordinal": ordinal,
                "text": col.extract_cloze_for_typing(note[field_name], ordinal),
            }
            for ordinal in sorted(note.cloze_numbers_in_fields())
        ]
    return entry


def compare_entries(col: Collection, note, spec: FixtureNote, notetype_name: str) -> list[dict]:
    kind, _, field_name = spec.expected_source.partition(":")
    results = []
    for expected, provided, combining in spec.compare_inputs:
        if kind == "field":
            assert expected == note[field_name], (note.guid, expected, note[field_name])
        else:
            extracted = {
                ordinal: col.extract_cloze_for_typing(note[field_name], ordinal)
                for ordinal in sorted(note.cloze_numbers_in_fields())
            }
            assert expected in extracted.values(), (note.guid, expected, extracted)
        results.append(
            {
                "notetype_name": notetype_name,
                "guid": note.guid,
                "expected": expected,
                "provided": provided,
                "combining": combining,
                "expected_source": spec.expected_source,
                "output": col.compare_answer(expected, provided, combining=combining),
            }
        )
    return results


def original_stock_kind_name(value: int) -> str | None:
    """Map Anki's enum to the name engram stores, or None for an unknown kind."""
    field = Notetype.Config.DESCRIPTOR.fields_by_name["original_stock_kind"]
    enum_value = field.enum_type.values_by_number.get(value)
    if enum_value is None:
        return None
    return {
        "ORIGINAL_STOCK_KIND_BASIC": "basic",
        "ORIGINAL_STOCK_KIND_BASIC_AND_REVERSED": "basic_and_reversed",
        "ORIGINAL_STOCK_KIND_BASIC_OPTIONAL_REVERSED": "basic_optional_reversed",
        "ORIGINAL_STOCK_KIND_BASIC_TYPING": "basic_typing",
        "ORIGINAL_STOCK_KIND_CLOZE": "cloze",
        "ORIGINAL_STOCK_KIND_IMAGE_OCCLUSION": "image_occlusion",
    }.get(enum_value.name)


def notetype_entry(notetype) -> dict:
    return {
        "name": notetype["name"],
        "kind": "cloze" if notetype["type"] == 1 else "normal",
        "css": notetype["css"],
        "sort_field_index": notetype["sortf"],
        "latex_pre": notetype["latexPre"],
        "latex_post": notetype["latexPost"],
        "latex_svg": notetype["latexsvg"],
        "original_stock_kind": original_stock_kind_name(notetype["originalStockKind"]),
        "fields": [
            {
                "name": field["name"],
                "ord": field["ord"],
                "sticky": field["sticky"],
                "rtl": field["rtl"],
                "font": field["font"],
                "font_size": field["size"],
                "description": field["description"],
                "plain_text": field["plainText"],
                "collapsed": field["collapsed"],
                "exclude_from_search": field["excludeFromSearch"],
                "tag": field["tag"],
                "prevent_deletion": field["preventDeletion"],
            }
            for field in notetype["flds"]
        ],
        "templates": [
            {
                "name": template["name"],
                "ord": template["ord"],
                "qfmt": template["qfmt"],
                "afmt": template["afmt"],
                "bqfmt": template["bqfmt"],
                "bafmt": template["bafmt"],
                "browser_font": template["bfont"],
                "browser_font_size": template["bsize"],
            }
            for template in notetype["tmpls"]
        ],
    }


def package_guids(package: Package) -> list[str]:
    if package.stock:
        return [spec.guid for notes in STOCK_NOTES.values() for spec in notes]
    return [spec.guid for nt in package.notetypes for spec in nt.notes]


def render_package(col: Collection, package: Package) -> dict:
    guids = package_guids(package)
    notes_by_guid = {}
    for nid in col.find_notes(""):
        note = col.get_note(nid)
        notes_by_guid[note.guid] = note

    note_specs = {}
    for name, specs in STOCK_NOTES.items():
        for spec in specs:
            note_specs[spec.guid] = (name, spec)
    for nt in package.notetypes:
        for spec in nt.notes:
            note_specs[spec.guid] = (nt.name, spec)

    golden_notes = []
    compare = []
    for guid in guids:
        note = notes_by_guid[guid]
        notetype_name, spec = note_specs[guid]
        golden_notes.append(render_note(col, note, notetype_name, spec))
        if spec.compare_inputs:
            compare.extend(compare_entries(col, note, spec, notetype_name))

    note_types = []
    for name in sorted({entry["notetype_name"] for entry in golden_notes}):
        note_types.append(notetype_entry(col.models.by_name(name)))

    return {
        "source": SOURCE,
        "fixture": package.fixture,
        "packages": ["%s.%s.apkg" % (package.fixture, fmt) for fmt in package.formats],
        "note_types": note_types,
        "notes": golden_notes,
        "compare_answers": compare,
    }


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------


def verify_package(path: Path, golden: dict, workdir: Path) -> None:
    """Import a written package into a fresh collection and compare its content.

    This is the only check that reads the three formats the way a client does:
    it fails if a normalised database or the rebuilt ZIP cannot be imported.
    """
    check_dir = workdir / ("verify-" + path.stem)
    check_dir.mkdir()
    col = Collection(str(check_dir / "check.anki2"))
    try:
        col.import_anki_package(
            ImportAnkiPackageRequest(
                package_path=str(path),
                options=ImportAnkiPackageOptions(
                    with_scheduling=False, with_deck_configs=False
                ),
            )
        )
        # The packages store note field values, not rendered output, so no real
        # host name may appear in them either.
        with zipfile.ZipFile(path) as archive:
            for info in archive.infolist():
                assert ANKI_DOCS_HOST.encode() not in archive.read(info.filename), (
                    path.name,
                    info.filename,
                )
        guids = sorted(col.get_note(nid).guid for nid in col.find_notes(""))
        expected_guids = sorted(note["guid"] for note in golden["notes"])
        assert guids == expected_guids, (path.name, guids, expected_guids)
        card_count = len(col.find_cards(""))
        expected_cards = sum(len(note["cards"]) for note in golden["notes"])
        assert card_count == expected_cards, (path.name, card_count, expected_cards)
        print("verified %s: %d notes, %d cards" % (path.name, len(guids), card_count))
    finally:
        col.close()


def replace_real_domain(text: str) -> str:
    return text.replace(ANKI_DOCS_HOST, PLACEHOLDER_DOCS_HOST)


def write_json(path: Path, data: dict) -> None:
    text = json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True)
    # Guards the rule that a committed fixture must not name a real domain: the
    # help-site host stays out, and the only documentation host in the file is
    # the placeholder. A future Anki release that adds another host fails the run
    # instead of being committed.
    assert ANKI_DOCS_HOST not in text, path.name
    assert text.count("https://docs.") == text.count(PLACEHOLDER_DOCS_HOST), path.name
    path.write_text(text + "\n", encoding="utf-8")


def print_table() -> None:
    print("| Rule id | Requirement | Fixture | Notes |")
    print("|---|---|---|---|")
    for rule, requirement, fixture, notes in RULE_ROWS:
        print("| `%s` | %s | %s | `%s` |" % (rule, requirement, fixture, notes))


def main(argv: list[str]) -> int:
    if "--table" in argv:
        print_table()
        return 0

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    workdir = Path(tempfile.mkdtemp(prefix="anki-oracle-"))
    try:
        collection_path = workdir / "fixtures.anki2"
        build_collection(collection_path)
        normalise_collection(collection_path)

        written: list[tuple[Path, dict]] = []
        col = Collection(str(collection_path))
        for package in PACKAGES:
            guids = package_guids(package)
            note_ids = []
            for nid in col.find_notes(""):
                note = col.get_note(nid)
                if note.guid in guids:
                    note_ids.append(nid)
            assert len(note_ids) == len(guids), (package.fixture, len(note_ids), len(guids))

            golden = render_package(col, package)
            write_json(OUT_DIR / ("%s.golden.json" % package.fixture), golden)

            for fmt in package.formats:
                raw = workdir / ("%s.%s.raw.apkg" % (package.fixture, fmt))
                col.export_anki_package(
                    out_path=str(raw),
                    options=ExportAnkiPackageOptions(
                        with_scheduling=False,
                        with_deck_configs=False,
                        with_media=True,
                        legacy=fmt in ("anki2", "anki21"),
                    ),
                    limit=NoteIdsLimit(note_ids=note_ids),
                )
                target = OUT_DIR / ("%s.%s.apkg" % (package.fixture, fmt))
                rewrite_package(raw, target, fmt=fmt, workdir=workdir)
                written.append((target, golden))
        col.close()

        for path, golden in written:
            verify_package(path, golden, workdir)
    finally:
        shutil.rmtree(workdir, ignore_errors=True)

    for path in sorted(OUT_DIR.iterdir()):
        if path.is_file():
            print("%s  %d" % (path.name, path.stat().st_size))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
