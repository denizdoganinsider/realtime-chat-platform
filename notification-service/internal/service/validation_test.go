package service

import (
	"errors"
	"testing"
)

func TestValidateWebhookURL(t *testing.T) {
	valid := []string{
		"https://hooks.example.com/chat",
		"http://example.com:8080/path?x=1",
	}
	for _, raw := range valid {
		if err := validateWebhookURL(raw, false); err != nil {
			t.Errorf("validateWebhookURL(%q) = %v, want nil", raw, err)
		}
	}

	invalid := map[string]string{
		"empty":        "",
		"relative":     "/hooks",
		"ftp":          "ftp://example.com/x",
		"credentials":  "https://user:pw@example.com/x",
		"loopback":     "http://127.0.0.1:9000/hook",
		"localhost":    "http://localhost:9000/hook",
		"private":      "http://10.0.0.5/hook",
		"link-local":   "http://169.254.169.254/latest/meta-data",
		"unspecified":  "http://0.0.0.0/hook",
		"ipv6 private": "http://[fd00::1]/hook",
	}
	for name, raw := range invalid {
		if err := validateWebhookURL(raw, false); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: validateWebhookURL(%q) = %v, want ErrValidation", name, raw, err)
		}
	}
}

// Local development registers a receiver on localhost; only loopback opens up,
// the rest of the private space stays closed.
func TestValidateWebhookURLLoopbackOptIn(t *testing.T) {
	if err := validateWebhookURL("http://localhost:9000/hook", true); err != nil {
		t.Errorf("loopback with opt-in rejected: %v", err)
	}
	if err := validateWebhookURL("http://127.0.0.1:9000/hook", true); err != nil {
		t.Errorf("127.0.0.1 with opt-in rejected: %v", err)
	}
	if err := validateWebhookURL("http://10.0.0.5/hook", true); !errors.Is(err, ErrValidation) {
		t.Errorf("private range accepted under loopback opt-in: %v", err)
	}
}
