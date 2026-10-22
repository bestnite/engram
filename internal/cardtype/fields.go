package cardtype

import "fmt"

// This file centralizes typed reads and validation of fields.
// fields come from JSON deserialization: numbers are float64, arrays are []any, objects are map[string]any
// (the behavior of the standard library's encoding/json); Go-side constructions such as []string are also supported.

// stringField reads a required non-empty string field.
func stringField(fields map[string]any, name string) (string, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return "", fmt.Errorf("missing required field %q", name)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q must be a string", name)
	}
	if s == "" {
		return "", fmt.Errorf("field %q must not be empty", name)
	}
	return s, nil
}

// stringSliceField reads a required non-empty string array field whose elements must be non-empty.
func stringSliceField(fields map[string]any, name string) ([]string, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return nil, fmt.Errorf("missing required field %q", name)
	}
	if ss, ok := v.([]string); ok {
		if len(ss) == 0 {
			return nil, fmt.Errorf("field %q must not be empty", name)
		}
		for i, s := range ss {
			if s == "" {
				return nil, fmt.Errorf("field %q[%d] must not be empty", name, i)
			}
		}
		return ss, nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("field %q must be an array of strings", name)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("field %q must not be empty", name)
	}
	out := make([]string, len(raw))
	for i, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("field %q[%d] must be a string", name, i)
		}
		if s == "" {
			return nil, fmt.Errorf("field %q[%d] must not be empty", name, i)
		}
		out[i] = s
	}
	return out, nil
}

// boolField reads an optional boolean field; returns the default def when missing.
func boolField(fields map[string]any, name string, def bool) (bool, error) {
	v, ok := fields[name]
	if !ok || v == nil {
		return def, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("field %q must be a boolean", name)
	}
	return b, nil
}

// validateCommonOptional validates the common optional fields extra / source_url.
//
// The frozen table lists them as optional fields available to every card type; whereas
// schema/note-import.schema.json puts them at the note level (siblings of fields).
// The two locations disagree, so this package accepts them inside fields per the frozen table and type-checks them;
// when placed at the note level they are not handled by this package. The discrepancy is documented in the delivery report and the schema was not changed.
func validateCommonOptional(fields map[string]any) error {
	if v, ok := fields["source_url"]; ok && v != nil {
		s, ok := v.(string)
		if !ok || s == "" {
			return fmt.Errorf(`field "source_url" must be a non-empty string`)
		}
	}
	if v, ok := fields["extra"]; ok && v != nil {
		if _, ok := v.(map[string]any); !ok {
			return fmt.Errorf(`field "extra" must be an object`)
		}
	}
	return nil
}
