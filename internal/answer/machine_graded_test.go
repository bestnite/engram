package answer

import (
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
)

// TestIsMachineGradedAsksTheControl pins the optional interface: the control,
// not a name switch in core code, decides whether Grade produces the rating.
func TestIsMachineGradedAsksTheControl(t *testing.T) {
	cases := []struct {
		name string
		spec *notetype.AnswerSpec
		want bool
	}{
		{"nil spec", nil, false},
		{"unknown control", &notetype.AnswerSpec{Control: "not_registered"}, false},
		{"self-assessed control", &notetype.AnswerSpec{Control: "short_answer"}, false},
		{"graded control", &notetype.AnswerSpec{Control: "text"}, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := IsMachineGraded(test.spec); got != test.want {
				t.Fatalf("IsMachineGraded(%+v) = %t, want %t", test.spec, got, test.want)
			}
		})
	}
}

// TestMachineGradedReadsTheOptionalInterface exercises the rule with no
// registry lookup, so a later self-assessed control only has to implement
// SelfAssessed: the core function already handles it.
func TestMachineGradedReadsTheOptionalInterface(t *testing.T) {
	if machineGraded(shortAnswerControl{}) {
		t.Error("short_answer was reported machine graded; the self-assessed interface was ignored")
	}
	if !machineGraded(textControl{}) {
		t.Error("text was reported self-assessed; a control without the interface must stay machine graded")
	}
}
