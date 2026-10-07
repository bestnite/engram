package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是分享同意制的验收：发邀请不发授权、接受才生效、拒绝不留痕，
// 以及接收策略（anyone / whitelist / nobody）在**邀请发出之前**就拦住。

func decodeShareInvites(t *testing.T, body []byte) spaShareInvitesResponse {
	t.Helper()
	var out spaShareInvitesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode share invites: %v", err)
	}
	return out
}

// TestShareInviteRequiresAcceptance 是同意制的核心：属主分享之后，被邀请者**还没有**任何授权，
// 直到他自己接受为止。
func TestShareInviteRequiresAcceptance(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Consent deck")
	invitee := newGrantee(t, srv, "invitee", "invitee@example.com")
	ctx := context.Background()
	grants := store.NewGrantStore(db)
	invites := store.NewDeckShareInviteStore(db)
	base := "/api/v1/decks/" + u64str(deck.ID) + "/sharing/grants"

	rec := jsonRequest(t, srv, http.MethodPost, base,
		`{"user_id":`+u64str(invitee.ID)+`,"role":"reader"}`, cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("POST grant = %d, want success (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	// 关键：授权**没有**出现，只有一条待接受的邀请。
	if role, err := grants.Role(ctx, deck.ID, invitee.ID); err != nil || role != "" {
		t.Fatalf("grant role = %q (err %v), want no grant before acceptance", role, err)
	}
	if _, err := invites.ByDeckAndUser(ctx, deck.ID, invitee.ID); err != nil {
		t.Fatalf("invite missing after the share: %v", err)
	}

	// 属主重复分享同一人同一角色：仍然是那一条邀请，不叠加。
	rec = jsonRequest(t, srv, http.MethodPost, base,
		`{"user_id":`+u64str(invitee.ID)+`,"role":"reader"}`, cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("second POST grant = %d, want success", rec.Code)
	}
	list, err := invites.ListForUser(ctx, invitee.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("invites for the recipient = %d (err %v), want exactly 1", len(list), err)
	}
	if list[0].DeckName != "Consent deck" {
		t.Errorf("invite deck name = %q, want the deck name for display", list[0].DeckName)
	}
}

// TestShareInviteAcceptAndReject 覆盖接受与拒绝两条出口。
func TestShareInviteAcceptAndReject(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Consent deck 2")
	// 被邀请者要自己登录来接受/拒绝，所以用「建号 + 登录」的夹具。
	inviteeID, inviteeCookies, inviteeCSRF := createUserAndLogin(t, srv, db, "invitee2")
	ctx := context.Background()
	grants := store.NewGrantStore(db)
	invites := store.NewDeckShareInviteStore(db)

	// 属主分享两次：一次会被接受，一次会被拒绝。
	other := seedReviewDeck(t, db, ownerID, "Consent deck 3")
	for _, d := range []*store.Deck{deck, other} {
		rec := jsonRequest(t, srv, http.MethodPost, "/api/v1/decks/"+u64str(d.ID)+"/sharing/grants",
			`{"user_id":`+u64str(inviteeID)+`,"role":"editor"}`, cookies, csrf)
		if rec.Code >= 300 {
			t.Fatalf("POST grant = %d, want success", rec.Code)
		}
	}

	// 列表里能看到两条。
	rec := getJSON(t, srv, "/api/v1/sharing/invites", inviteeCookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET invites = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	pending := decodeShareInvites(t, rec.Body.Bytes())
	if len(pending.Invites) != 2 {
		t.Fatalf("pending invites = %d, want 2", len(pending.Invites))
	}
	if pending.Policy != store.ShareAcceptAnyone {
		t.Errorf("policy = %q, want anyone by default", pending.Policy)
	}

	// 接受第一条：授权出现、邀请消失。
	rec = jsonRequest(t, srv, http.MethodPost, "/api/v1/sharing/invites/"+u64str(deck.ID)+"/accept", "", inviteeCookies, inviteeCSRF)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if role, err := grants.Role(ctx, deck.ID, inviteeID); err != nil || role != store.RoleEditor {
		t.Fatalf("grant role after accept = %q (err %v), want editor", role, err)
	}
	if _, err := invites.ByDeckAndUser(ctx, deck.ID, inviteeID); err == nil {
		t.Error("invite should be gone after acceptance")
	}

	// 拒绝第二条：邀请消失，授权从未出现。
	rec = jsonRequest(t, srv, http.MethodPost, "/api/v1/sharing/invites/"+u64str(other.ID)+"/reject", "", inviteeCookies, inviteeCSRF)
	if rec.Code != http.StatusOK {
		t.Fatalf("reject = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if role, err := grants.Role(ctx, other.ID, inviteeID); err != nil || role != "" {
		t.Errorf("grant role after reject = %q (err %v), want none", role, err)
	}
	// 接受与拒绝都写审计。
	for _, action := range []string{store.ActionDeckShareAccept, store.ActionDeckShareReject} {
		if n, err := store.NewAuditStore(db).CountByAction(ctx, action); err != nil || n != 1 {
			t.Errorf("audit rows for %s = %d (err %v), want 1", action, n, err)
		}
	}
}

// TestSharePolicyRefusesInvites 覆盖拒收策略：nobody 与 whitelist 都在**发邀请之前**拦住。
func TestSharePolicyRefusesInvites(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Refused deck")
	invitee := newGrantee(t, srv, "refuser", "refuser@example.com")
	ctx := context.Background()
	policy := store.NewSharePolicyStore(db)
	invites := store.NewDeckShareInviteStore(db)
	path := "/api/v1/decks/" + u64str(deck.ID) + "/sharing/grants"
	body := `{"user_id":` + u64str(invitee.ID) + `,"role":"reader"}`

	// nobody：直接拒绝，且不留邀请。
	if err := policy.SetPolicy(ctx, invitee.ID, store.ShareAcceptNobody); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	rec := jsonRequest(t, srv, http.MethodPost, path, body, cookies, csrf)
	if rec.Code != http.StatusConflict {
		t.Fatalf("grant to a refusing recipient = %d, want 409 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if _, err := invites.ByDeckAndUser(ctx, deck.ID, invitee.ID); err == nil {
		t.Error("no invite should be created for a recipient who refuses shares")
	}

	// whitelist：不在名单里同样拒绝，加进名单后放行。
	if err := policy.SetPolicy(ctx, invitee.ID, store.ShareAcceptWhitelist); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	if rec := jsonRequest(t, srv, http.MethodPost, path, body, cookies, csrf); rec.Code != http.StatusConflict {
		t.Fatalf("grant with an empty whitelist = %d, want 409", rec.Code)
	}
	if err := policy.Allow(ctx, invitee.ID, ownerID); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if rec := jsonRequest(t, srv, http.MethodPost, path, body, cookies, csrf); rec.Code >= 300 {
		t.Fatalf("grant from a whitelisted owner = %d, want success (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	// 默认（anyone）放行。
	other := newGrantee(t, srv, "open-user", "open-user@example.com")
	if rec := jsonRequest(t, srv, http.MethodPost, path,
		`{"user_id":`+u64str(other.ID)+`,"role":"reader"}`, cookies, csrf); rec.Code >= 300 {
		t.Fatalf("grant to a default-policy recipient = %d, want success", rec.Code)
	}
}

// TestSharePolicySaveEndpoint 覆盖策略保存端点：校验非法值、写审计、白名单增删。
func TestSharePolicySaveEndpoint(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	other := newGrantee(t, srv, "allowee", "allowee@example.com")

	rec := jsonRequest(t, srv, http.MethodPut, "/api/v1/settings/share-policy",
		`{"policy":"sometimes"}`, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid policy = %d, want 400", rec.Code)
	}
	rec = jsonRequest(t, srv, http.MethodPut, "/api/v1/settings/share-policy",
		`{"policy":"whitelist","allow":[`+u64str(other.ID)+`]}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT policy = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	allow, err := store.NewSharePolicyStore(db).AllowList(context.Background(), ownerID)
	if err != nil || len(allow) != 1 || allow[0] != other.ID {
		t.Fatalf("allow list = %v (err %v), want [%d]", allow, err, other.ID)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionSharePolicyUpdate); err != nil || n != 1 {
		t.Errorf("audit rows = %d (err %v), want 1", n, err)
	}
	// 用户名写法（界面用这个）：认得的名字进名单，不认识的名字整单拒绝且不改任何状态。
	rec = jsonRequest(t, srv, http.MethodPut, "/api/v1/settings/share-policy",
		`{"allow_usernames":["allowee"]}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT allow_usernames = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var after map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode policy response: %v", err)
	}
	// 响应回读的服务端真值：白名单带用户名，界面不必再查一次。
	if !strings.Contains(string(after["allow_list"]), "allowee") {
		t.Errorf("allow_list = %s, want the resolved username", after["allow_list"])
	}
	rec = jsonRequest(t, srv, http.MethodPut, "/api/v1/settings/share-policy",
		`{"allow_usernames":["nobody-by-this-name"]}`, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown username = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 移出白名单。
	rec = jsonRequest(t, srv, http.MethodPut, "/api/v1/settings/share-policy",
		`{"revoke":[`+u64str(other.ID)+`]}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT revoke = %d, want 200", rec.Code)
	}
	allow, err = store.NewSharePolicyStore(db).AllowList(context.Background(), ownerID)
	if err != nil || len(allow) != 0 {
		t.Errorf("allow list after revoke = %v (err %v), want empty", allow, err)
	}
}
