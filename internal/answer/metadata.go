package answer

import (
	"encoding/json"
	"strconv"

	"git.nite07.com/nite/engram/internal/notetype"
)

// MetadataFieldValue resolves the reserved field by name, not by an assumed ID.
// Its value is raw JSON, not rich text: HTML wrappers and entities are not decoded
// or repaired, so invalid content stays unchanged and disables alternatives.
func MetadataFieldValue(cfg notetype.Config, values map[string]string) string {
	for _, field := range cfg.Fields {
		if field.Name == "engram_meta" {
			return values[field.ID]
		}
	}
	return ""
}

// ClozeAlternatives reads only the requested subtree, so malformed sibling
// capabilities, numbers or wordings cannot disable a valid answer list.
func ClozeAlternatives(raw string, number int, canonical string) []string {
	object := func(data []byte) map[string]json.RawMessage {
		var out map[string]json.RawMessage
		if json.Unmarshal(data, &out) != nil {
			return nil
		}
		return out
	}
	root := object([]byte(raw))
	capability := object(root["cloze_accept"])
	group := object(capability[strconv.Itoa(number)])
	value := group[canonical]
	var entries []json.RawMessage
	if len(value) == 0 || string(value) == "null" || json.Unmarshal(value, &entries) != nil {
		return nil
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		var text string
		// A mixed-type array invalidates this wording's list, not sibling lists.
		if string(entry) == "null" || json.Unmarshal(entry, &text) != nil {
			return nil
		}
		result = append(result, text)
	}
	return result
}
