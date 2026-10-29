package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/worker"
)

// Outbox is the mail queue and the background worker.
//
// Contract:
//   - Enqueue only writes the outbox queue table and wakes the worker; it never sends synchronously
//     on the request path.
//   - When SMTP is not configured, Enqueue returns ErrNotConfigured and does not drop the mail
//     silently.
//   - Delivery failures are retried by the worker with backoff, and the last error and the attempt
//     count can be read from the admin panel.
//
// Concurrency model: a single worker goroutine consumes serially; there is no "single concurrency"
// semantic constraint -- the optimizer's single concurrency does not apply to mail, otherwise one
// parameter optimization would block every mail behind it.

// RetryPolicy controls the backoff retries: after the n-th failure it waits BaseDelay*2^(n-1),
// capped at MaxDelay; once the attempt count reaches MaxAttempts the message is marked permanently
// failed.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetryPolicy is the production default: at most 5 attempts, starting at 1s, waiting at most
// 5 minutes.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: 5 * time.Minute}
}

// backoff returns how long to wait after the given attempt has failed.
func (p RetryPolicy) backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := p.BaseDelay
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= p.MaxDelay {
			return p.MaxDelay
		}
	}
	if delay > p.MaxDelay {
		return p.MaxDelay
	}
	return delay
}

// Deps are the explicit dependencies of Outbox.
type Deps struct {
	// DB is required (the outbox queue table).
	DB *gorm.DB
	// Secrets is used to decrypt the SMTP password; when nil the encrypted password cannot be read
	// (SMTP auth counts as not configured).
	Secrets *store.SecretCodec
	// Logger is optional; when nil, slog.Default() is used.
	Logger *slog.Logger
	// Sender is the delivery transport; when nil, SMTPSender{} is used. Tests inject a stand-in to
	// avoid real SMTP.
	Sender Sender
	// LookupEnv can be given an environment variable stand-in; when nil, os.LookupEnv is used.
	LookupEnv func(string) (string, bool)
	// Now can be given a clock; when nil, the system UTC time is used.
	Now func() time.Time
	// Retry is the backoff policy; a zero value means DefaultRetryPolicy().
	Retry RetryPolicy
	// PollInterval is the worker polling interval; <=0 means DefaultPollInterval.
	PollInterval time.Duration
	// BatchSize is the maximum number of mails delivered per round; <=0 means DefaultBatchSize.
	BatchSize int
}

const (
	// DefaultPollInterval is the default polling interval of the worker.
	DefaultPollInterval = 5 * time.Second
	// DefaultBatchSize is the default delivery batch per round.
	DefaultBatchSize = 20
)

// Outbox holds the queue, the configuration resolver and the background worker.
type Outbox struct {
	db       *gorm.DB
	resolver *Resolver
	sender   Sender
	logger   *slog.Logger
	now      func() time.Time
	retry    RetryPolicy
	poll     time.Duration
	batch    int

	// wake is the signal with which Enqueue wakes the worker; capacity 1, non-blocking send.
	wake chan struct{}
	// life is the shared start/stop and tick skeleton (internal/worker); drain is its round, wake its extra trigger.
	life *worker.Lifecycle
}

// NewOutbox builds an Outbox; it does not start the worker, call Start for that.
// The signature is fixed to *Outbox by the interface contract (no error return): cmd/engram
// guarantees the dependencies are present at assembly time, and a missing DB is reported explicitly
// by Enqueue/Start at runtime instead of giving the constructor one more branch every caller has to
// handle.
func NewOutbox(deps Deps) *Outbox {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	sender := deps.Sender
	if sender == nil {
		sender = SMTPSender{}
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	retry := deps.Retry
	if retry.MaxAttempts <= 0 {
		retry = DefaultRetryPolicy()
	}
	if retry.BaseDelay <= 0 {
		retry.BaseDelay = time.Second
	}
	if retry.MaxDelay <= 0 {
		retry.MaxDelay = 5 * time.Minute
	}
	poll := deps.PollInterval
	if poll <= 0 {
		poll = DefaultPollInterval
	}
	batch := deps.BatchSize
	if batch <= 0 {
		batch = DefaultBatchSize
	}
	o := &Outbox{
		db:       deps.DB,
		resolver: NewResolver(deps.DB, deps.Secrets),
		sender:   sender,
		logger:   logger,
		now:      now,
		retry:    retry,
		poll:     poll,
		batch:    batch,
		wake:     make(chan struct{}, 1),
	}
	life, err := worker.New(worker.Config{
		RunOnce:     func(ctx context.Context) error { o.drain(ctx); return nil },
		Logger:      logger,
		FailMessage: "mail: outbox round failed",
		Interval:    poll,
		Wake:        o.wake,
	})
	if err != nil {
		// Every required field is set just above, so this only fires if the worker package changes its contract.
		panic("mail: build outbox worker: " + err.Error())
	}
	o.life = life
	return o
}

// Configured reports whether SMTP is configured. When it is not, flows that depend on mail must
// disable themselves and say why. It reads the configuration fresh every time (the settings table may
// have just been changed by an admin), hence no context parameter.
func (o *Outbox) Configured() bool {
	_, ok, err := o.resolver.Config(context.Background())
	if err != nil {
		o.logger.Error("mail: resolve smtp config failed", "error", err)
		return false
	}
	return ok
}

// Enqueue writes one mail into the outbox queue and wakes the worker.
//
// It only writes the queue table and does not send on the request path: delivery is done by the
// worker and failures are not reported back to the caller. When SMTP is not configured it returns
// ErrNotConfigured (no silent drop).
func (o *Outbox) Enqueue(ctx context.Context, m Message) error {
	if o.db == nil {
		return errors.New("mail: outbox database is required")
	}
	if strings.TrimSpace(m.To) == "" {
		return errors.New("mail: message recipient is required")
	}
	if _, ok, err := o.resolver.Config(ctx); err != nil {
		return fmt.Errorf("mail: resolve smtp config: %w", err)
	} else if !ok {
		return ErrNotConfigured
	}
	headers := m.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	encoded, err := json.Marshal(headers)
	if err != nil {
		return fmt.Errorf("mail: encode headers: %w", err)
	}
	row := &store.OutboxMessage{
		To:          strings.TrimSpace(m.To),
		Type:        m.Type,
		Subject:     m.Subject,
		TextBody:    m.TextBody,
		HTMLBody:    m.HTMLBody,
		HeadersJSON: string(encoded),
	}
	if err := store.EnqueueOutboxMessage(ctx, o.db, row, o.now()); err != nil {
		return err
	}
	o.notify()
	return nil
}

// notify wakes the worker without blocking; if a signal is already pending it returns immediately.
func (o *Outbox) notify() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// Start launches the single worker goroutine; repeated calls are idempotent. The worker exits when
// ctx is canceled. Without a database it returns immediately: there is no queue table to deliver from,
// and starting a worker would only report errors over and over in the polling loop. The first round runs
// at once, because a previous process may have left due pending mail behind; later rounds run on every
// poll and whenever Enqueue wakes the worker.
func (o *Outbox) Start(ctx context.Context) {
	if o.db == nil {
		o.logger.Error("mail: outbox has no database; worker not started")
		return
	}
	o.life.Start(ctx)
}

// Stop stops the worker gracefully: it cancels the context and waits for the current round to finish.
// Safe to call repeatedly.
func (o *Outbox) Stop() {
	o.life.Stop()
}

// drain delivers every due mail. When SMTP is not configured it returns immediately: pending mail
// stays in the queue until the configuration is complete, instead of being wrongly marked failed
// (otherwise one missing configuration would burn through the whole backlog).
func (o *Outbox) drain(ctx context.Context) {
	if _, ok, err := o.resolver.Config(ctx); err != nil {
		o.logger.Error("mail: resolve smtp config failed", "error", err)
		return
	} else if !ok {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		rows, err := store.DueOutboxMessages(ctx, o.db, o.now(), o.batch)
		if err != nil {
			o.logger.Error("mail: load due outbox messages failed", "error", err)
			return
		}
		if len(rows) == 0 {
			return
		}
		for _, row := range rows {
			if ctx.Err() != nil {
				return
			}
			o.deliver(ctx, row)
		}
		if len(rows) < o.batch {
			return
		}
	}
}

// deliver delivers one mail and writes the state back according to the result: success -> sent;
// failure -> rescheduled with backoff, or permanently failed.
func (o *Outbox) deliver(ctx context.Context, row store.OutboxMessage) {
	cfg, ok, err := o.resolver.Config(ctx)
	if err != nil {
		o.logger.Error("mail: resolve smtp config failed", "outbox_id", row.ID, "error", err)
		return
	}
	if !ok {
		return
	}
	sendErr := o.sender.Send(ctx, cfg, messageFromRow(row))
	attempts := row.Attempts + 1
	if sendErr == nil {
		if err := store.MarkOutboxSent(ctx, o.db, row.ID, o.now()); err != nil {
			o.logger.Error("mail: mark outbox sent failed", "outbox_id", row.ID, "error", err)
		}
		return
	}
	o.logger.Warn("mail: delivery failed", "outbox_id", row.ID, "attempts", attempts, "error", sendErr)
	if attempts >= o.retry.MaxAttempts {
		if err := store.MarkOutboxFailed(ctx, o.db, row.ID, attempts, sendErr.Error(), o.now()); err != nil {
			o.logger.Error("mail: mark outbox failed failed", "outbox_id", row.ID, "error", err)
		}
		return
	}
	next := o.now().Add(o.retry.backoff(attempts))
	if err := store.MarkOutboxRetry(ctx, o.db, row.ID, attempts, next, sendErr.Error(), o.now()); err != nil {
		o.logger.Error("mail: reschedule outbox failed", "outbox_id", row.ID, "error", err)
	}
}

// messageFromRow turns an outbox row back into a Message (for delivery).
func messageFromRow(row store.OutboxMessage) Message {
	headers := map[string]string{}
	if strings.TrimSpace(row.HeadersJSON) != "" {
		_ = json.Unmarshal([]byte(row.HeadersJSON), &headers)
	}
	return Message{
		To:       row.To,
		Type:     row.Type,
		Subject:  row.Subject,
		TextBody: row.TextBody,
		HTMLBody: row.HTMLBody,
		Headers:  headers,
	}
}
