package store

import (
	"reflect"

	"git.nite07.com/nite/engram/internal/notetype"
)

// noteTypeDerivedPlan records which note-level derived data a note type update must rebuild.
//
// Cards, notes.search_text and media_notes are derived from a subset of Config. A save that
// changes only presentation data leaves all three unchanged, so the update can skip the walk
// over the type's notes entirely. The comparison is conservative: a changed input marks the
// whole derived table for rebuild even when a particular note would end up with the same row.
type noteTypeDerivedPlan struct {
	// SearchText marks notes.search_text stale. noteSearchText reads the field order, the ids,
	// the names and the search flags; a note's stored values do not change during an update.
	SearchText bool
	// MediaNotes marks media_notes stale. mediaFieldValues copies every stored field value and
	// removes only the ids of fields named MetadataFieldName, so the mapping changes only when
	// that set of ids changes.
	MediaNotes bool
	// Cards marks the stored card set stale. Card generation reads the fields and the template
	// fronts, and the sync also restores a card whose row was soft-deleted while its template is
	// still declared. Any field or template change therefore resyncs, and the cards of all notes
	// are read in bounded batches rather than one SELECT per note.
	Cards bool
}

// loadsNotes reports whether any derived work needs the note rows. A plan that marks nothing
// leaves the whole type untouched below the row and the note-type media reference set.
func (p noteTypeDerivedPlan) loadsNotes() bool {
	return p.SearchText || p.MediaNotes || p.Cards
}

// planNoteTypeDerived compares the stored and the new config and names the derived data that
// has to be rebuilt. Both arguments must be the configs as they are written, so the comparison
// sees every normalisation the write path applies.
func planNoteTypeDerived(oldCfg, newCfg notetype.Config) noteTypeDerivedPlan {
	return noteTypeDerivedPlan{
		SearchText: noteTypeSearchInputsChanged(oldCfg, newCfg),
		MediaNotes: noteTypeMetadataFieldIDsChanged(oldCfg, newCfg),
		Cards:      noteTypeCardInputsChanged(oldCfg, newCfg),
	}
}

// noteTypeSearchInputsChanged reports whether noteSearchText can produce a different value.
//
// noteSearchText visits the fields in config order and reads each field's id, name and
// ExcludeFromSearch; it skips the metadata field by name. A change to any other field
// property, or to anything outside Config.Fields, does not reach the search index.
func noteTypeSearchInputsChanged(oldCfg, newCfg notetype.Config) bool {
	if len(oldCfg.Fields) != len(newCfg.Fields) {
		return true
	}
	for i := range oldCfg.Fields {
		if !sameSearchField(oldCfg.Fields[i], newCfg.Fields[i]) {
			return true
		}
	}
	return false
}

func sameSearchField(a, b notetype.Field) bool {
	return a.ID == b.ID && a.Name == b.Name && a.ExcludeFromSearch == b.ExcludeFromSearch
}

// noteTypeMetadataFieldIDsChanged reports whether mediaFieldValues would remove a different set
// of field ids. It keeps every stored value and deletes only the fields named MetadataFieldName,
// so the media mapping is a function of the note's stored values and this id set alone. Renaming
// a field into or out of the metadata role is what changes it; adding, removing or reordering any
// other field leaves the mapping as it was.
func noteTypeMetadataFieldIDsChanged(oldCfg, newCfg notetype.Config) bool {
	oldIDs := metadataFieldIDs(oldCfg)
	newIDs := metadataFieldIDs(newCfg)
	if len(oldIDs) != len(newIDs) {
		return true
	}
	for id := range newIDs {
		if _, ok := oldIDs[id]; !ok {
			return true
		}
	}
	return false
}

func metadataFieldIDs(cfg notetype.Config) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, field := range cfg.Fields {
		if field.Name == notetype.MetadataFieldName {
			ids[field.ID] = struct{}{}
		}
	}
	return ids
}

// noteTypeCardInputsChanged reports whether an update has to resync the stored cards of the type.
//
// Card generation itself reads only part of the config: the fields, each template's front format,
// the template order for normal ordinals and the kind. The sync does more than generation, though:
// it restores a row that was soft-deleted while its template is still declared, and it realigns a
// card's ordinal. A stored card can be soft-deleted while no template was removed, for example by
// the empty-card cleanup, so any change to the fields or the templates is treated as a reason to
// resync. Only the parts of the config that never reach card generation or card identity leave the
// cards alone: the CSS, the note type name, the LaTeX settings and the sort field. Field order alone
// cannot change a generated card either, but it is compared too, because a needless sync is safe and
// a missed one is not.
func noteTypeCardInputsChanged(oldCfg, newCfg notetype.Config) bool {
	return oldCfg.Kind != newCfg.Kind ||
		!reflect.DeepEqual(oldCfg.Fields, newCfg.Fields) ||
		!reflect.DeepEqual(oldCfg.Templates, newCfg.Templates)
}
