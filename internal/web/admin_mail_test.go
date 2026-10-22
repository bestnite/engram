package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the acceptance test for (class D admin notification mail).
//
// Covers: the two trigger points that really exist in this repository each deliver the correct type of mail to the **admin's sign-in mailbox**;
// "a mail failure does not affect the triggering operation"; class D can be turned off; the page renders mail.not_configured when SMTP is unconfigured.

// attachAdminMail writes effective SMTP settings and attaches an outbox to the test service; the worker is not started,
// so enqueued mail stays in the outbox table and the test can query it directly by type (the same approach as the class A tests).
func attachAdminMail(t *testing.T, srv *Server, db *gorm.DB) {
	t.Helper()
	configureSMTP(t, db)
	srv.mail = mail.NewOutbox(mail.Deps{DB: db, Logger: discardLogger()})
}

// waitOutboxType polls until a mail of the given type appears in the outbox (the job-failure hook fires asynchronously inside the worker).
func waitOutboxType(t *testing.T, db *gorm.DB, typ mail.Type) []store.OutboxMessage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if rows := outboxByType(t, db, typ); len(rows) > 0 {
			return rows
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %s mail enqueued within the timeout", typ)
	return nil
}

// TestAdminMailJobFailureReachesAdmin trigger point one: after a job failure the admin receives job_failed.
// This runs a real Runner (/bin/sh -c 'exit 3') and hooks it up to the web layer's NotifyJobFailed,
// so it covers the full chain of the jobs.OnFailure hook and delivery.
func TestAdminMailJobFailureReachesAdmin(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	attachAdminMail(t, srv, db)

	runner, err := jobs.New(jobs.Deps{
		DB: db, Logger: discardLogger(),
		Command: func(context.Context, *store.Job, jobs.Reporter) (jobs.Command, error) {
			return jobs.Command{Name: "/bin/sh", Args: []string{"-c", "exit 3"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("jobs.New: %v", err)
	}
	runner.SetOnFailure(srv.NotifyJobFailed)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runner.Start(ctx)

	if _, err := runner.Enqueue(ctx, jobs.KindOptimize, nil); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	rows := waitOutboxType(t, db, mail.TypeJobFailed)
	if rows[0].To != "owner@example.com" {
		t.Errorf("job_failed recipient = %q, want owner@example.com", rows[0].To)
	}
	t.Logf("failed job triggered %s mail to admin %s (subject %q)", rows[0].Type, rows[0].To, rows[0].Subject)
}

// TestAdminMailMediaQuotaReachesAdmin trigger point two: when a user upload is blocked by the media quota, the admin receives media_disk_alert.
func TestAdminMailMediaQuotaReachesAdmin(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	attachAdminMail(t, srv, db)
	t.Setenv(envMediaUserQuotaBytes, "")
	ctx := context.Background()
	if err := store.PutSetting(ctx, db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting(media_user_quota_bytes): %v", err)
	}
	deck := seedDeck(t, db, ownerID, "quota deck")

	// First fill up 1500 bytes: upload + have the note reference it.
	first := uploadMedia(t, srv, cookies, csrf, "m1.png", "image/png", quotaPNG(1500))
	if first.Code != http.StatusCreated {
		t.Fatalf("first upload = %d, want 201", first.Code)
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode first upload: %v", err)
	}
	referenceMedia(t, db, deck.ID, ownerID, saved.Sha256)

	// Then upload 1000 more: 1500+1000 > 2048 → 413, which triggers the admin alert.
	rec := uploadMedia(t, srv, cookies, csrf, "m2.png", "image/png", quotaPNG(1000))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota upload = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}

	rows := outboxByType(t, db, mail.TypeMediaDiskAlert)
	if len(rows) != 1 {
		t.Fatalf("media_disk_alert rows = %d, want 1", len(rows))
	}
	if rows[0].To != "owner@example.com" {
		t.Errorf("media_disk_alert recipient = %q, want owner@example.com", rows[0].To)
	}
	t.Logf("quota rejection triggered %s mail to admin %s (subject %q)", rows[0].Type, rows[0].To, rows[0].Subject)
}

// TestAdminMailHonorsAdminPreference proves class D can be turned off: once the receiving admin turns off a type they no longer receive that class of mail,
// but the triggering operation still succeeds. Verified via the media quota trigger point (synchronous, deterministic, no dependency on the async worker).
func TestAdminMailHonorsAdminPreference(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	attachAdminMail(t, srv, db)
	if err := store.NewEmailPrefStore(db).SetChoices(context.Background(), ownerID,
		map[string]bool{string(mail.TypeMediaDiskAlert): false}, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices: %v", err)
	}

	t.Setenv(envMediaUserQuotaBytes, "")
	if err := store.PutSetting(context.Background(), db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting(media_user_quota_bytes): %v", err)
	}
	deck := seedDeck(t, db, ownerID, "quota deck")

	first := uploadMedia(t, srv, cookies, csrf, "m1.png", "image/png", quotaPNG(1500))
	if first.Code != http.StatusCreated {
		t.Fatalf("first upload = %d, want 201", first.Code)
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode first upload: %v", err)
	}
	referenceMedia(t, db, deck.ID, ownerID, saved.Sha256)

	// Trigger the quota: the admin has turned off that type → the upload is still rejected (413), but no mail is sent.
	rec := uploadMedia(t, srv, cookies, csrf, "m2.png", "image/png", quotaPNG(1000))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota upload = %d, want 413 (admin opted out)", rec.Code)
	}
	if rows := outboxByType(t, db, mail.TypeMediaDiskAlert); len(rows) != 0 {
		t.Errorf("media_disk_alert rows = %d, want 0 (admin opted out)", len(rows))
	}
}

// brokenMailOutbox constructs an outbox that "reports configured but always fails to enqueue": env vars provide host/from
// so Configured() is true, while an empty DB makes Enqueue return an error. Used to deterministically verify "a mail failure does not affect the triggering operation".
func brokenMailOutbox(t *testing.T) *mail.Outbox {
	t.Helper()
	t.Setenv(mail.EnvSMTPHost, "smtp.test")
	t.Setenv(mail.EnvSMTPFrom, "noreply@test")
	ob := mail.NewOutbox(mail.Deps{DB: nil, Logger: discardLogger()})
	if !ob.Configured() {
		t.Fatal("fixture outbox should report configured; the enqueue path would not be exercised")
	}
	return ob
}

// TestAdminMailFailureDoesNotBreakTrigger is the core counter-case: when enqueueing fails, the media quota block, this triggering
// operation, still succeeds (413) and never fails because of mail.
func TestAdminMailFailureDoesNotBreakTrigger(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	srv.mail = brokenMailOutbox(t)

	t.Setenv(envMediaUserQuotaBytes, "")
	if err := store.PutSetting(context.Background(), db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting(quota): %v", err)
	}
	deck := seedDeck(t, db, ownerID, "quota deck")
	first := uploadMedia(t, srv, cookies, csrf, "m1.png", "image/png", quotaPNG(1500))
	if first.Code != http.StatusCreated {
		t.Fatalf("first upload = %d, want 201", first.Code)
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode first upload: %v", err)
	}
	referenceMedia(t, db, deck.ID, ownerID, saved.Sha256)
	if rec := uploadMedia(t, srv, cookies, csrf, "m2.png", "image/png", quotaPNG(1000)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota upload with a broken mailer = %d, want 413", rec.Code)
	}
	t.Logf("mail enqueue failed on the quota trigger, yet the upload was still rejected with 413")
}

// TestAdminMailCarriesUnsubscribeHeaders proves class-D mail leaves the outbox with the RFC 8058 headers:
// class D is optional mail, so the receiving admin must be able to turn it off without logging in.
func TestAdminMailCarriesUnsubscribeHeaders(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	attachAdminMail(t, srv, db)
	srv.baseURL = "https://example.com"

	srv.NotifyMediaAlert(context.Background(), &store.User{ID: 9001, Username: "heavy-user", Email: "heavy@example.com"}, 2<<20, 4<<20)

	rows := waitOutboxType(t, db, mail.TypeMediaDiskAlert)
	var headers map[string]string
	if err := json.Unmarshal([]byte(rows[0].HeadersJSON), &headers); err != nil {
		t.Fatalf("parse HeadersJSON %q: %v", rows[0].HeadersJSON, err)
	}
	link := strings.TrimSpace(headers["List-Unsubscribe"])
	if link == "" {
		t.Fatalf("class-D mail carries no List-Unsubscribe header: %v", headers)
	}
	if got, want := headers["List-Unsubscribe-Post"], "List-Unsubscribe=One-Click"; got != want {
		t.Errorf("List-Unsubscribe-Post = %q, want %q", got, want)
	}
	if !strings.HasPrefix(strings.Trim(link, "<>"), "https://example.com/unsubscribe?token=") {
		t.Errorf("List-Unsubscribe = %q, want an absolute BASE_URL unsubscribe link", link)
	}
	tok, err := srv.tokens.Peek(context.Background(), store.ActionTokenUnsubscribe, tokenFromUnsubscribeHeader(t, link))
	if err != nil {
		t.Fatalf("peek unsubscribe token: %v", err)
	}
	if tok.Payload != string(mail.TypeMediaDiskAlert) {
		t.Errorf("unsubscribe token payload = %q, want %q", tok.Payload, mail.TypeMediaDiskAlert)
	}
}
