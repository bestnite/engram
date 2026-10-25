package cardtype

import "testing"

// TestRenderOptionsParallelToOptionsField pins the contract the review page relies on when it draws answer controls: a card type
// that declares Describe().OptionsField returns exactly that field's entries in Render(...).Options, in stored order, and no type
// returns options on the back side.
//
// Both halves matter. The review page builds one answer control per entry of Options and maps the clicked display slot back to a
// stored option index, so an Options list that is not the stored one — a different order, a reworded entry, extra lines — would
// display or grade the wrong option while still compiling.
func TestRenderOptionsParallelToOptionsField(t *testing.T) {
	for _, kind := range Kinds() {
		t.Run(kind, func(t *testing.T) {
			typ, ok := Lookup(kind)
			if !ok {
				t.Fatalf("Lookup(%q) not found", kind)
			}
			d := typ.Describe()
			// Example is documented as a set of field values that passes Validate, which is what makes it usable as a sample note.
			cards, err := Cards(Note{Kind: kind, Fields: d.Example})
			if err != nil {
				t.Fatalf("Cards from the example fields: %v", err)
			}
			if len(cards) == 0 {
				t.Fatal("Cards from the example fields = none")
			}
			for i, card := range cards {
				front, err := typ.Render(card, SideFront)
				if err != nil {
					t.Fatalf("Render(front, card %d): %v", i, err)
				}
				back, err := typ.Render(card, SideBack)
				if err != nil {
					t.Fatalf("Render(back, card %d): %v", i, err)
				}
				if len(back.Options) != 0 {
					t.Errorf("card %d back Options = %v, want none", i, back.Options)
				}
				if d.OptionsField == "" {
					if len(front.Options) != 0 {
						t.Errorf("card %d front Options = %v, but the type declares no OptionsField", i, front.Options)
					}
					continue
				}
				want, err := stringSliceField(d.Example, d.OptionsField)
				if err != nil {
					t.Fatalf("example field %q: %v", d.OptionsField, err)
				}
				if len(front.Options) != len(want) {
					t.Fatalf("card %d front Options = %v, want one entry per stored option %v", i, front.Options, want)
				}
				for j := range want {
					if front.Options[j] != want[j] {
						t.Errorf("card %d front Options[%d] = %q, want the stored option %q", i, j, front.Options[j], want[j])
					}
				}
			}
		})
	}
}
