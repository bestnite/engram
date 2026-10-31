#!/usr/bin/env python3
"""Probe Anki's filtered-deck scheduling and package export."""
import sys
import sqlite3
import zipfile
from datetime import datetime, timezone
from pathlib import Path

from anki.collection import Collection
from anki.import_export_pb2 import ExportAnkiPackageOptions, ExportLimit

root = Path(sys.argv[1])
root.mkdir(parents=True, exist_ok=True)
col = Collection(str(root / "source.anki2"))
model = col.models.by_name("Basic")
for label in ("learning", "review", "relearning"):
    note = col.new_note(model)
    note["Front"] = label
    note["Back"] = label
    col.add_note(note, 1)
ids = col.find_cards("")
today = col.sched._timing_today().days_elapsed
for cid, typ in zip(ids, (1, 2, 3)):
    card = col.get_card(cid)
    card.type = typ
    card.queue = 3 if typ != 2 else 2
    card.due = today + 3
    card.ivl = 3
    card.factor = 2500
    col.update_card(card)
did = col.decks.new_filtered("Filtered")
deck = col.decks.get(did)
deck["terms"] = [["deck:Default", 100, 0]]
col.decks.save(deck)
col.sched.rebuild_filtered_deck(did)
crt = col.db.scalar("select crt from col")
print("source crt", crt, "today", today)
for row in col.db.all("select type, queue, due, odue, odid from cards order by type"):
    print("filtered", *row)
col.export_anki_package(out_path=str(root / "filtered.apkg"), options=ExportAnkiPackageOptions(with_scheduling=True, with_deck_configs=True, with_media=False, legacy=True), limit=ExportLimit())
col.close()
with zipfile.ZipFile(root / "filtered.apkg") as package:
    database = root / "export.sqlite"
    database.write_bytes(package.read("collection.anki21"))
with sqlite3.connect(database) as db:
    print("package crt", db.execute("select crt from col").fetchone()[0])
    for row in db.execute("select type, queue, due, odue, odid from cards order by type"):
        print("package", *row, "expected due", datetime.fromtimestamp(crt + row[3] * 86400, timezone.utc).isoformat())
