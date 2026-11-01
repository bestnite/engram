"""Generate a modern Anki package with non-default FSRS parameters."""
from pathlib import Path
from tempfile import TemporaryDirectory

from anki.collection import Collection
from anki.import_export_pb2 import ExportAnkiPackageOptions, ExportLimit

OUT = Path(__file__).resolve().parents[2] / "internal/schedule/testdata/weights.anki21b.apkg"
WEIGHTS = [0.5, 1.5, 2.5, 3.5, 4.5, 3.5, 3.5, 0.05, 1.0, 0.2, 0.8, 1.5, 0.06, 0.3, 1.6, 0.6, 1.9, 0.5, 0.1, 0.07, 0.15]

with TemporaryDirectory() as directory:
    col = Collection(str(Path(directory) / "collection.anki2"))
    config = col.decks.get_config(1)
    config["fsrsParams6"] = WEIGHTS
    col.decks.update_config(config)
    note = col.new_note(col.models.by_name("Basic"))
    note["Front"] = "question"
    note["Back"] = "answer"
    col.add_note(note, 1)
    OUT.parent.mkdir(parents=True, exist_ok=True)
    col.export_anki_package(out_path=str(OUT), options=ExportAnkiPackageOptions(
        with_scheduling=True, with_deck_configs=True, with_media=False, legacy=False), limit=ExportLimit())
    col.close()
    print(f"Generated {OUT.name} with {len(WEIGHTS)} FSRS parameters")
