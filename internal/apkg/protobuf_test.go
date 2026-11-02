package apkg

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// TestDecodeNoteTypeConfigReadsAnswerSpecsFromOther pins the modern-layout path: Anki
// keeps unknown top-level model keys in Notetype.Config.other, so a note type that
// travelled through an Anki import and a modern re-export still carries its engram key.
func TestDecodeNoteTypeConfigReadsAnswerSpecsFromOther(t *testing.T) {
	other := []byte(`{"engram":{"v":1,"answer_specs":{"Card 1":{"control":"text","bindings":{"answer":"Back"}}}}}`)
	config := protowire.AppendTag(nil, 1, protowire.VarintType)
	config = protowire.AppendVarint(config, 0)
	config = protowire.AppendTag(config, 3, protowire.BytesType)
	config = protowire.AppendBytes(config, []byte(".card {}"))
	config = protowire.AppendTag(config, 255, protowire.BytesType)
	config = protowire.AppendBytes(config, other)

	cfg, err := decodeNoteTypeConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CSS != ".card {}" {
		t.Fatalf("css = %q, want %q", cfg.CSS, ".card {}")
	}
	if !cfg.AnswerSpecsKey {
		t.Fatal("AnswerSpecsKey = false, want true for a config carrying the engram key")
	}
	want := map[string]AnswerSpec{
		"Card 1": {Control: "text", Bindings: map[string]string{"answer": "Back"}},
	}
	if !reflect.DeepEqual(cfg.AnswerSpecs, want) {
		t.Fatalf("AnswerSpecs = %+v, want %+v", cfg.AnswerSpecs, want)
	}
}

// TestAnswerSpecsFromOther covers the unknown-key data the reader may meet: only a
// version-1 engram object yields a usable answer-spec key, and anything else is ignored
// rather than failing the package.
func TestAnswerSpecsFromOther(t *testing.T) {
	want := map[string]AnswerSpec{
		"Card 1": {Control: "numeric", Bindings: map[string]string{"value": "Back"}},
	}
	for _, tc := range []struct {
		name  string
		other string
		specs map[string]AnswerSpec
		key   bool
	}{
		{
			name:  "engram key beside another unknown key",
			other: `{"other_key":{"a":1},"engram":{"v":1,"answer_specs":{"Card 1":{"control":"numeric","bindings":{"value":"Back"}}}}}`,
			specs: want,
			key:   true,
		},
		{name: "no other field", other: ""},
		{name: "no engram key", other: `{"other_key":{"a":1}}`},
		{name: "not a JSON object", other: `["engram"]`},
		{name: "not JSON", other: "engram"},
		{name: "engram is not an object", other: `{"engram":1}`},
		{name: "unknown version", other: `{"engram":{"v":2,"answer_specs":{}}}`},
		{name: "empty object", other: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			specs, key := answerSpecsFromOther([]byte(tc.other))
			if key != tc.key {
				t.Fatalf("answerSpecsFromOther(%q) key = %v, want %v", tc.other, key, tc.key)
			}
			if !reflect.DeepEqual(specs, tc.specs) {
				t.Fatalf("answerSpecsFromOther(%q) specs = %+v, want %+v", tc.other, specs, tc.specs)
			}
		})
	}
}
