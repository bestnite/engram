#!/usr/bin/env python3
"""Open a produced .apkg with the pinned Anki release and print what it contains.

Usage:
    tools/anki-oracle/verify_package.py path/to/package.apkg

The script imports the package into a fresh temporary collection through Anki's own
importer and prints its note, card, note type, media and deck-config counts. Answer
specs are an engram extension carried in an unknown note type key; Anki's public model
API does not expose unknown keys, so they are counted from the package's own schema-11
model JSON.
"""

from __future__ import annotations

import json
import os
import sqlite3
import sys
import tempfile
import zipfile

from anki.collection import Collection
from anki.import_export_pb2 import ImportAnkiPackageRequest


def count_answer_specs(package_path: str) -> int:
    """Count answer specs in the package's legacy collection.anki21 model JSON."""
    with zipfile.ZipFile(package_path) as archive:
        name = None
        for candidate in ("collection.anki21", "collection.anki2"):
            if candidate in archive.namelist():
                name = candidate
                break
        if name is None:
            return 0
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "collection.sqlite")
            with open(path, "wb") as out:
                out.write(archive.read(name))
            connection = sqlite3.connect(path)
            try:
                row = connection.execute("SELECT models FROM col").fetchone()
            finally:
                connection.close()
    if not row or not row[0]:
        return 0
    models = json.loads(row[0])
    total = 0
    for model in models.values():
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
            print(f"answer_specs: {count_answer_specs(package_path)}")
        finally:
            collection.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())