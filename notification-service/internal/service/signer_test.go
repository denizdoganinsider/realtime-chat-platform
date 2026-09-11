package service

import "testing"

func TestSignAndVerifyRoundTrip(t *testing.T) {
	body := []byte(`{"event":"message.created"}`)
	signature := Sign("secret", 1700000000, body)

	if !Verify("secret", 1700000000, body, signature) {
		t.Fatal("a freshly produced signature did not verify")
	}
	if len(signature) != len("sha256=")+64 {
		t.Errorf("signature %q has unexpected length", signature)
	}
}

// Each of these is a distinct attack the MAC has to defeat: a changed body, a
// replay at a different time, a different secret, a truncated signature.
func TestVerifyRejectsTampering(t *testing.T) {
	body := []byte(`{"event":"message.created"}`)
	signature := Sign("secret", 1700000000, body)

	cases := map[string]bool{
		"body changed":      Verify("secret", 1700000000, []byte(`{"event":"message.deleted"}`), signature),
		"timestamp changed": Verify("secret", 1700000001, body, signature),
		"wrong secret":      Verify("other", 1700000000, body, signature),
		"truncated":         Verify("secret", 1700000000, body, signature[:len(signature)-1]),
		"empty":             Verify("secret", 1700000000, body, ""),
	}

	for name, ok := range cases {
		if ok {
			t.Errorf("%s: Verify accepted a bad signature", name)
		}
	}
}
