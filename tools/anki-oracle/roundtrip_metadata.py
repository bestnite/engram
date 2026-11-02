#!/usr/bin/env python3
"""Import an engram package with the pinned Anki, then export both layouts."""

import importlib.metadata
import os
import sys
import tempfile

from anki.collection import Collection
from anki.import_export_pb2 import ExportAnkiPackageOptions, ExportLimit, ImportAnkiPackageRequest


def main():
    if importlib.metadata.version("anki") != "26.9.3":
        raise RuntimeError("anki==26.9.3 is required")
    source, legacy, modern = map(os.path.abspath, sys.argv[1:])
    with tempfile.TemporaryDirectory() as tmp:
        col = Collection(os.path.join(tmp, "collection.anki2"))
        try:
            col.import_anki_package(ImportAnkiPackageRequest(package_path=source))
            if col.note_count() != 1:
                raise RuntimeError(f"expected one note, got {col.note_count()}")
            for target, old in ((legacy, True), (modern, False)):
                col.export_anki_package(out_path=target, options=ExportAnkiPackageOptions(
                    with_scheduling=False, with_deck_configs=False, with_media=False, legacy=old), limit=ExportLimit())
            print("anki=26.9.3 imported=1 exported=legacy,modern")
        finally:
            col.close()


if __name__ == "__main__":
    main()
