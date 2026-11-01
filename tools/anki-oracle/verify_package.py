#!/usr/bin/env python3
"""Open a produced .apkg with the pinned Anki release and print what it contains.

Usage:
    tools/anki-oracle/verify_package.py path/to/package.apkg

The script imports the package into a fresh temporary collection through Anki's own
importer and prints its note, card, note type, media and deck-config counts. Answer
specs are an engram extension carried in an unknown note type key. They are counted in
the collection Anki built from the package after the import, not in the input package's
own model JSON: counting the input only restates what the caller passed in, while the
imported collection is what Anki actually produced.
"""

from __future__ import annotations

import os
import sys
import tempfile

from anki.collection import Collection
from anki.import_export_pb2 import ImportAnkiPackageRequest


def count_answer_specs(collection: Collection) -> int:
    """Count answer specs on the note types of the collection Anki imported."""
    total = 0
    for model in collection.models.all():
        engram = model.get("engram") or {}
        if engram.get("v") == 1:
            total += len(engram.get("answer_specs") or {})
    return total


def main() -> int:
    if len(sys.argv) != 2:
        print(f"usage: {sys.argv[0]} <package.apkg>", file=sys.stderr)
        return 2
    package_path = os.path.abspath(sys.argv[1])
    if not os.path.isfile(package_path):
        print(f"not a file: {package_path}", file=sys.stderr)
        return 2

    with tempfile.TemporaryDirectory() as tmp:
        collection = Collection(os.path.join(tmp, "collection.anki2"))
        try:
            request = ImportAnkiPackageRequest(package_path=package_path)
            request.options.with_scheduling = True
            request.options.with_deck_configs = True
            collection.import_anki_package(request)
            note_types = collection.models.all()
            configs = collection.decks.all_config()
            media = os.listdir(collection.media.dir())
            print(f"notes: {collection.note_count()}")
            print(f"cards: {collection.card_count()}")
            print(f"note_types: {len(note_types)}")
            print(f"note_type_names: {', '.join(sorted(nt['name'] for nt in note_types))}")
            print(f"media: {len(media)}")
            print(f"media_names: {', '.join(sorted(media))}")
            print(f"deck_configs: {len(configs)}")
            print(f"deck_config_names: {', '.join(sorted(cfg['name'] for cfg in configs))}")
            print(f"answer_specs: {count_answer_specs(collection)}")
        finally:
            collection.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())