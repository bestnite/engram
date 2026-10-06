package auth

import (
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 本地与 OIDC 建号共用这条准备路径，不能在创建时把午夜替换成默认值。
func TestNewUserCutoffPreservesUnsetAndMidnight(t *testing.T) {
	for _, hour := range []*int{nil, store.Ptr(0), store.Ptr(23)} {
		u, err := newUserFromInput(CreateUserInput{Username: "cutoff", Email: "cutoff@example.com", DayCutoffHour: hour}, nil, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if hour == nil {
			if u.DayCutoffHour != nil {
				t.Fatal("unset changed")
			}
		} else if u.DayCutoffHour == nil || *u.DayCutoffHour != *hour {
			t.Fatal("midnight changed")
		}
	}
	for _, hour := range []int{-1, 24} {
		if _, err := newUserFromInput(CreateUserInput{Username: "cutoff", Email: "cutoff@example.com", DayCutoffHour: store.Ptr(hour)}, nil, time.Now().UTC()); err == nil {
			t.Fatalf("accepted hour %d", hour)
		}
	}
}
