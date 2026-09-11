package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

const (
	SignatureHeader = "X-Signature"
	TimestampHeader = "X-Timestamp"
	EventIDHeader   = "X-Event-ID"
)

// Sign computes the value of X-Signature for a delivery: HMAC-SHA256 over
// "<unix timestamp>.<raw body>", hex encoded, prefixed with the scheme.
//
// The timestamp is inside the MAC on purpose. Signing the body alone proves
// the body came from us, but lets anyone who captured one delivery replay it
// forever; a receiver that rejects timestamps older than a few minutes closes
// that. This is the Stripe/GitHub shape, and month 2's service-to-service
// calls settled for a plain shared key over localhost precisely because this
// is where signed bodies belong.
func Sign(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify is what a receiver runs. It is here, next to Sign, so the two cannot
// drift - and so the verification receiver in the README can be checked
// against it.
func Verify(secret string, timestamp int64, body []byte, signature string) bool {
	expected := Sign(secret, timestamp, body)
	return hmac.Equal([]byte(expected), []byte(signature))
}
