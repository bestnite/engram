package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 公开接口必须保留字段级错误码，不能把描述拒绝退化为通用请求错误。
func TestCreateDeckDescriptionLimit(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	u := seedUser(t, env.db, "description_api", store.RoleUser)
	k := seedKey(t, env.keys, u.ID, []string{store.ScopeWrite}, nil)
	for _, n := range []int{2000, 2001} {
		body, _ := json.Marshal(map[string]string{"name": "description", "description": strings.Repeat("汉", n)})
		status, raw := doJSON(t, env.router(), http.MethodPost, "/api/v1/decks", k.Plaintext, string(body))
		if n == 2000 {
			if status != http.StatusCreated {
				t.Fatalf("valid description status = %d: %s", status, raw)
			}
			continue
		}
		var out struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if status != http.StatusBadRequest || out.Error.Code != CodeDeckDescriptionInvalid {
			t.Fatalf("invalid description = %d %s", status, raw)
		}
	}
}
