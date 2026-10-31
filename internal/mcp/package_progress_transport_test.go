package mcp

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestImportToolsProgress(t *testing.T) {
	raw, err := os.ReadFile("../notetype/testdata/anki/stock.anki21b.apkg")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"import_deck", "create_import_upload"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/enabled=%t", tool, enabled), func(t *testing.T) {
				_, db, keys, ts := newEnv(t)
				u := seedUser(t, db, "progress")
				key := newKey(t, keys, u.ID, []string{store.ScopeWrite})
				cs := connect(t, ts.URL, key)
				args := map[string]any{"target": "new_deck"}
				if enabled {
					args["include_progress"] = true
				}
				if tool == "import_deck" {
					args["package"] = base64.StdEncoding.EncodeToString(raw)
				}
				out, isErr, text := callTool(t, cs, tool, args)
				if isErr {
					t.Fatal(text)
				}
				if tool == "create_import_upload" {
					req, err := http.NewRequest(http.MethodPut, ts.URL+out["upload_url"].(string), bytes.NewReader(raw))
					if err != nil {
						t.Fatal(err)
					}
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil || resp.StatusCode != http.StatusOK {
						t.Fatalf("upload %d %s %v", resp.StatusCode, body, err)
					}
				}
				var count int64
				if err := db.Model(&store.CardState{}).Where("user_id = ?", u.ID).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if (count > 0) != enabled {
					t.Fatalf("states = %d, enabled = %t", count, enabled)
				}
			})
		}
	}
}
