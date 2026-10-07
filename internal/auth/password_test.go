package auth

import (
	"strings"
	"testing"
)

// fastParams 是测试用弱参数：只验证格式与参数随哈希记录，不追求抗爆破强度，避免拖慢测试。
func fastParams() Params {
	return Params{Memory: 1024, Time: 1, Threads: 1, SaltLength: 8, KeyLength: 16}
}

func TestPasswordVerify(t *testing.T) {
	h := NewPasswordHasher(fastParams())
	encoded, err := h.Hash("correct horse battery")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	cases := []struct {
		name     string
		password string
		want     bool
	}{
		{name: "correct password", password: "correct horse battery", want: true},
		{name: "wrong password", password: "correct horse batteru", want: false},
		{name: "empty password", password: "", want: false},
		{name: "case differs", password: "Correct horse battery", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Verify(encoded, tc.password)
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("Verify(%q) = %v, want %v", tc.password, got, tc.want)
			}
		})
	}
}

func TestHashStoresParametersWithVersion(t *testing.T) {
	p := fastParams()
	h := NewPasswordHasher(p)
	encoded, err := h.Hash("versioned-secret")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	// 带版本的编码格式是"参数随哈希入库"的验收点。
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("hash %q does not start with the versioned argon2id prefix", encoded)
	}
	got, _, _, err := decodeHash(encoded)
	if err != nil {
		t.Fatalf("decodeHash() error = %v", err)
	}
	if got.Memory != p.Memory || got.Time != p.Time || got.Threads != p.Threads {
		t.Errorf("decoded params = %+v, want memory=%d time=%d threads=%d",
			got, p.Memory, p.Time, p.Threads)
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	cases := []struct {
		name   string
		hash   string
		reason string
	}{
		{name: "not argon2id", hash: "$2b$10$abcdefghijklmnopqrstuv", reason: "bcrypt is not accepted"},
		{name: "unknown version", hash: "$argon2id$v=18$m=1024,t=1,p=1$AAAA$BBBB", reason: "old version rejected"},
		{name: "bad params", hash: "$argon2id$v=19$m=xx,t=1,p=1$AAAA$BBBB", reason: "unparsable params"},
		{name: "short field count", hash: "$argon2id$v=19$m=1024,t=1,p=1", reason: "missing salt and key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Verify(tc.hash, "whatever"); err == nil {
				t.Errorf("Verify(%q) error = nil, want error (%s)", tc.hash, tc.reason)
			}
		})
	}
}

func TestValidatePasswordPolicy(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "valid", password: "Tr0ub4dor&3", wantErr: false},
		{name: "too short", password: "short1", wantErr: true},
		{name: "too long", password: strings.Repeat("a", MaxPasswordLength+1), wantErr: true},
		{name: "common password", password: "password123", wantErr: true},
		{name: "common password with case", password: "Password123", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePasswordPolicy(tc.password)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidatePasswordPolicy(%q) error = %v, wantErr %v", tc.password, err, tc.wantErr)
			}
		})
	}
}
