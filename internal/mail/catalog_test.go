package mail

import (
	"strings"
	"testing"
)

// TestCatalogClassesAndDefaults 锁定 DESIGN.md §4.7 的四类与默认值：
// A 不可关闭、默认开；B 默认开、可关；C 默认关、可关；D 默认开、可关。
func TestCatalogClassesAndDefaults(t *testing.T) {
	cases := []struct {
		class       Class
		wantTypes   int
		wantDisable bool
		wantDefault bool
	}{
		{ClassSecurity, 5, false, true},
		{ClassCollab, 3, true, true},
		{ClassStudy, 5, true, false},
		{ClassAdmin, 2, true, true},
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
	}
}

// TestClassOrderIsStable 断言大类的展示顺序是 A → B → C → D，偏好页据此渲染分组。
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

// TestCatalogTypesUniqueAndLookup 断言类型标识唯一，且每个目录项都能被 Lookup 取回。
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

// TestResolveEnabledLocksClassA 是「拒绝关闭 A 类」的核心保证：
// 即便存储里出现 A 类型的显式关闭，ResolveEnabled 也必须返回 true。
func TestResolveEnabledLocksClassA(t *testing.T) {
	// 伪造一份「把 A 类全关掉」的选择，模拟被篡改或历史遗留的存储。
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

// TestResolveEnabledHonorsOptionalChoices 断言可关闭类型：显式选择优先，缺席时回落到默认。
func TestResolveEnabledHonorsOptionalChoices(t *testing.T) {
	choices := map[string]bool{
		string(TypeDeckShared):     false, // B 默认开 → 用户关掉
		string(TypeReviewReminder): true,  // C 默认关 → 用户打开
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

// TestResolveEnabledUnknownTypeIsNotSent 断言未登记类型一律不发送：拼错类型名不能变成静默投递。
func TestResolveEnabledUnknownTypeIsNotSent(t *testing.T) {
	if ResolveEnabled(nil, "typo_type") {
		t.Error("ResolveEnabled enabled an unknown type; unknown types must never be sent")
	}
	if ResolveEnabled(map[string]bool{"typo_type": true}, "typo_type") {
		t.Error("ResolveEnabled enabled an unknown type even with an explicit true choice")
	}
}
