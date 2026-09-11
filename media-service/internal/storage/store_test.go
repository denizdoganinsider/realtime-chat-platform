package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

// The 8-byte PNG signature plus padding: enough for http.DetectContentType.
func pngBytes(size int) []byte {
	b := make([]byte, size)
	copy(b, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	return b
}

func TestPutIsContentAddressed(t *testing.T) {
	s, err := NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	content := pngBytes(1024)
	meta, err := s.Put(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	sum := sha256.Sum256(content)
	if meta.Hash != hex.EncodeToString(sum[:]) {
		t.Errorf("Hash = %s, want the sha256 of the bytes", meta.Hash)
	}
	if meta.ContentType != "image/png" || meta.Size != 1024 {
		t.Errorf("Meta = %+v", meta)
	}

	again, err := s.Put(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("second Put returned error: %v", err)
	}
	if again.Hash != meta.Hash {
		t.Error("the same bytes got a different address")
	}
}

func TestOpenReturnsTheBytes(t *testing.T) {
	s, err := NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	content := pngBytes(700)
	meta, _ := s.Put(bytes.NewReader(content))

	got, file, err := s.Open(meta.Hash)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer file.Close()

	read, _ := io.ReadAll(file)
	if !bytes.Equal(read, content) || got.ContentType != "image/png" {
		t.Error("Open returned different bytes or type than were stored")
	}
}

func TestRejectsOversized(t *testing.T) {
	s, err := NewStore(t.TempDir(), 1000)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Put(bytes.NewReader(pngBytes(1001))); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Put error = %v, want ErrTooLarge", err)
	}
	if _, err := s.Put(bytes.NewReader(pngBytes(1000))); err != nil {
		t.Errorf("Put at exactly the cap returned error: %v", err)
	}
}

// The sniffed type decides, not the uploader's claim: HTML bytes are refused
// however they were labelled.
func TestRejectsNonImage(t *testing.T) {
	s, err := NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Put(bytes.NewReader([]byte("<html><script>alert(1)</script></html>"))); !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("Put error = %v, want ErrUnsupportedType", err)
	}
}

func TestOpenRejectsNonHashNames(t *testing.T) {
	s, err := NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"../../etc/passwd", "abc", "ZZZZ"} {
		if _, _, err := s.Open(name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Open(%q) error = %v, want ErrNotFound", name, err)
		}
	}
}
