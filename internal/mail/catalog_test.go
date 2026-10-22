package mail

import (
	"strings"
	"testing"
)

// TestCatalogClassesAndDefaults locks the four classes of the mail catalog and their defaults:
// A cannot be disabled and defaults on; B defaults on, disableable; C defaults off, disableable; D
// defaults on, disableable, admin-only.
func TestCatalogClassesAndDefaults(t *testing.T) {
	cases := []struct {
		class         Class
		wantTypes     int
		wantDisable   bool
		wantDefault   bool
		wantAdminOnly bool
	}{
		{ClassSecurity, 5, false, true, false},
		{ClassCollab, 3, true, true, false},
		// Class C dropped from 5 types to 3 on 2026-10-07: streak about to break and import/export done
		// were moved out of the catalog for having no trigger point (the reasoning is in the comment in
		// catalog.go). This number changes with the catalog; update it here when the catalog changes.
		{ClassStudy, 3, true, false, false},
		{ClassAdmin, 2, true, true, true},
	}
	counts := map[Class]int{}
	for _, def := range Catalog() {
		counts[def.Class]++
	}
	for _, tc := range cases {
		if got := counts[tc.class]; got != tc.wantTypes {
			t.Errorf("class %q has %d types, want %d", tc.class, got, tc.wantTypes)
		}
		if got := CanDisable(tc.class); got != tc.wantDisable {
			t.Errorf("CanDisable(%q) = %v, want %v", tc.class, got, tc.wantDisable)
		}
		if got := DefaultEnabled(tc.class); got != tc.wantDefault {
			t.Errorf("DefaultEnabled(%q) = %v, want %v", tc.class, got, tc.wantDefault)
		}
		if got := AdminOnly(tc.class); got != tc.wantAdminOnly {
			t.Errorf("AdminOnly(%q) = %v, want %v", tc.class, got, tc.wantAdminOnly)
		}
	}
}

// TestClassOrderIsStable asserts that the display order of the categories is A -> B -> C -> D, which
// is what the preferences page renders its groups from.
func TestClassOrderIsStable(t *testing.T) {
	want := []Class{ClassSecurity, ClassCollab, ClassStudy, ClassAdmin}
	got := ClassOrder()
	if len(got) != len(want) {
		t.Fatalf("ClassOrder() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ClassOrder()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCatalogTypesUniqueAndLookup asserts that type identifiers are unique and that every catalog
// entry can be fetched back by Lookup.
func TestCatalogTypesUniqueAndLookup(t *testing.T) {
	seen := map[Type]bool{}
	for _, def := range Catalog() {
		if seen[def.Type] {
			t.Errorf("duplicate type %q in catalog", def.Type)
		}
		seen[def.Type] = true
		if def.Type == "" || def.Class == "" || def.LabelKey == "" {
			t.Errorf("catalog entry %+v has an empty field", def)
		}
		if !strings.HasPrefix(def.LabelKey, "mail.prefs.type.") {
			t.Errorf("type %q label key %q does not use the mail.prefs.type. prefix", def.Type, def.LabelKey)
		}
		if want := "mail.prefs.type." + string(def.Type); def.LabelKey != want {
			t.Errorf("type %q label key = %q, want %q", def.Type, def.LabelKey, want)
		}
		got, ok := Lookup(def.Type)
		if !ok {
			t.Fatalf("Lookup(%q) not found though it is in the catalog", def.Type)
		}
		if got != def {
			t.Errorf("Lookup(%q) = %+v, want %+v", def.Type, got, def)
		}
	}
	if _, ok := Lookup("not_a_real_type"); ok {
		t.Error("Lookup accepted an unknown type")
	}
}

// TestResolveEnabledLocksClassA is the core guarantee of "refuse to turn class A off": even if an
// explicit off for an A type appears in storage, ResolveEnabled must return true.
func TestResolveEnabledLocksClassA(t *testing.T) {
	// Forge a choice that "turns every class A type off", simulating tampered or legacy storage.
	choices := map[string]bool{
		string(TypePasswordReset):  false,
		string(TypeNewDeviceLogin): false,
		string(TypeAccountStatus):  false,
	}
	for _, def := range Catalog() {
		if def.Class != ClassSecurity {
			continue
		}
		if !ResolveEnabled(choices, def.Type) {
			t.Errorf("ResolveEnabled turned off class A type %q; class A must never be disableable", def.Type)
		}
	}
}

// TestResolveEnabledHonorsOptionalChoices asserts the disableable types: an explicit choice wins, and
// when absent it falls back to the default.
func TestResolveEnabledHonorsOptionalChoices(t *testing.T) {
	choices := map[string]bool{
		string(TypeDeckShared):     false, // B defaults on -> the user turns it off
		string(TypeReviewReminder): true,  // C defaults off -> the user turns it on
	}
	cases := []struct {
		typ  Type
		want bool
		why  string
	}{
		{TypeDeckShared, false, "B explicit off wins over default on"},
		{TypeReviewReminder, true, "C explicit on wins over default off"},
		{TypeInvite, true, "B absent falls back to default on"},
		{TypeStudyDigest, false, "C absent falls back to default off"},
		{TypeJobFailed, true, "D absent falls back to default on"},
		{TypeMediaDiskAlert, true, "D absent falls back to default on"},
	}
	for _, tc := range cases {
		if got := ResolveEnabled(choices, tc.typ); got != tc.want {
			t.Errorf("ResolveEnabled(%q) = %v, want %v (%s)", tc.typ, got, tc.want, tc.why)
		}
	}
}

// TestResolveEnabledUnknownTypeIsNotSent asserts that unregistered types are never sent: a mistyped
// type name must not turn into a silent delivery.
func TestResolveEnabledUnknownTypeIsNotSent(t *testing.T) {
	if ResolveEnabled(nil, "typo_type") {
		t.Error("ResolveEnabled enabled an unknown type; unknown types must never be sent")
	}
	if ResolveEnabled(map[string]bool{"typo_type": true}, "typo_type") {
		t.Error("ResolveEnabled enabled an unknown type even with an explicit true choice")
	}
}
