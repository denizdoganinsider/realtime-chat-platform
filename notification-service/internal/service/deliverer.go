package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"realtime-chat-platform/notification-service/internal/domain"
	"realtime-chat-platform/notification-service/internal/repository"
)

const (
	deliveryMaxRetries     = 3
	deliveryRequestTimeout = 5 * time.Second
	// A receiver's error body is logged, so it is capped: a misbehaving endpoint
	// must not be able to fill the log with a megabyte per attempt.
	maxErrorBodyBytes = 256
)

// Deliverer makes the HTTP call to a webhook and records what happened. It is
// the predecessor's WebhookDeliveryService with three changes that are the
// actual month 4 lesson: the body is signed, the backoff has jitter and is
// interruptible, and every attempt lands in the deliveries table instead of
// only the final failure landing in a log line.
type Deliverer struct {
	deliveryRepo repository.DeliveryRepositoryInterface
	httpClient   *http.Client
	now          func() time.Time
	sleep        func(ctx context.Context, attempt int) error
}

func NewDeliverer(deliveryRepo repository.DeliveryRepositoryInterface, allowLoopback bool) *Deliverer {
	return &Deliverer{
		deliveryRepo: deliveryRepo,
		httpClient: &http.Client{
			Timeout: deliveryRequestTimeout,
			// Resolves and filters every destination at dial time; see ssrf.go.
			Transport: newSafeTransport(allowLoopback),
			// A receiver that answers with a redirect to somewhere else is not
			// the endpoint the user registered. Deliver to the URL as given.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now:   time.Now,
		sleep: sleepWithBackoff,
	}
}

// Deliver POSTs the event to one recipient with retries. It never returns an
// error: the outcome is recorded on the delivery row, which is the contract.
func (d *Deliverer) Deliver(ctx context.Context, recipient Recipient, event domain.MessageEvent) {
	payload := domain.WebhookPayload{
		Event:     "message.created",
		EventID:   event.EventID,
		Recipient: recipient.Webhook.UserID,
		Data:      event,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		d.record(recipient.Delivery.ID, domain.DeliveryStatusFailed, 0, nil, "encoding payload: "+err.Error())
		return
	}

	var lastStatus *int
	var lastErr string
	attempts := 0

	for attempt := 0; attempt <= deliveryMaxRetries; attempt++ {
		if attempt > 0 {
			if err := d.sleep(ctx, attempt); err != nil {
				lastErr = "shutdown before retry"
				break
			}
		}
		attempts++

		status, err := d.attempt(ctx, recipient.Webhook, body)
		if err != nil {
			lastErr = err.Error()
			lastStatus = nil
			d.record(recipient.Delivery.ID, domain.DeliveryStatusPending, attempts, nil, lastErr)
			continue
		}

		lastStatus = &status
		if status >= 200 && status < 300 {
			d.record(recipient.Delivery.ID, domain.DeliveryStatusDelivered, attempts, lastStatus, "")
			return
		}

		lastErr = fmt.Sprintf("receiver answered status=%d", status)
		// A 4xx is the receiver saying "no" to this body; sending it again will
		// not change its mind. Only 5xx and transport failures are retried.
		if status < 500 {
			break
		}
		d.record(recipient.Delivery.ID, domain.DeliveryStatusPending, attempts, lastStatus, lastErr)
	}

	slog.Warn("webhook delivery failed",
		"service", "notification-service",
		"delivery_id", recipient.Delivery.ID,
		"user_id", recipient.Webhook.UserID,
		"attempts", attempts,
		"error", lastErr,
	)
	d.record(recipient.Delivery.ID, domain.DeliveryStatusFailed, attempts, lastStatus, lastErr)
}

func (d *Deliverer) attempt(ctx context.Context, webhook domain.Webhook, body []byte) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}

	timestamp := d.now().Unix()
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "realtime-chat-notification/1.0")
	request.Header.Set(TimestampHeader, strconv.FormatInt(timestamp, 10))
	request.Header.Set(SignatureHeader, Sign(webhook.Secret, timestamp, body))

	response, err := d.httpClient.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	// Drain (bounded) so the keep-alive connection is reusable.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBodyBytes))

	return response.StatusCode, nil
}

func (d *Deliverer) record(id int64, status domain.DeliveryStatus, attempts int, lastStatus *int, lastErr string) {
	var errPtr *string
	if lastErr != "" {
		if len(lastErr) > 512 {
			lastErr = lastErr[:512]
		}
		errPtr = &lastErr
	}

	if err := d.deliveryRepo.RecordAttempt(id, status, attempts, lastStatus, errPtr); err != nil {
		slog.Error("failed to record delivery attempt",
			"service", "notification-service", "delivery_id", id, "error", err)
	}
}

// Backoff is 1s, 2s, 4s plus up to 50% jitter, interruptible by ctx - the same
// shape as chat-service's presence client. The predecessor's time.Sleep in a
// loop would hold a worker hostage through shutdown.
func sleepWithBackoff(ctx context.Context, attempt int) error {
	base := time.Duration(1<<uint(attempt-1)) * time.Second
	delay := base + rand.N(base/2)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
