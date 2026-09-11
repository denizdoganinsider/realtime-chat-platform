// Package storage is a content-addressed file store on local disk. The address
// of a file is the SHA-256 of its bytes, which is the property everything in
// month 4's CDN layer rests on: the same bytes always have the same URL, so a
// cached copy can never be stale - there is nothing to invalidate, only new
// content at a new URL.
package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

var ErrNotFound = errors.New("not found")
var ErrTooLarge = errors.New("file too large")
var ErrUnsupportedType = errors.New("unsupported content type")

// Images only: this is an avatar/attachment store, and serving arbitrary
// uploaded bytes (HTML, SVG with scripts) from the same origin as the API is
// how a file store becomes an XSS vector.
var allowedTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Meta struct {
	Hash        string `json:"hash"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

type Store struct {
	dir      string
	maxBytes int64
}

func NewStore(dir string, maxBytes int64) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir, maxBytes: maxBytes}, nil
}

// Put streams the upload to a temp file while hashing it, then renames it into
// place under its hash. Streaming means the size cap is enforced without ever
// holding the whole file in memory; the rename means a reader can never see a
// half-written object. Storing the same bytes twice is a no-op.
func (s *Store) Put(r io.Reader) (*Meta, error) {
	// Sniff the type from the first 512 bytes rather than trusting the
	// client's Content-Type: the header is whatever the uploader says.
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	head = head[:n]

	contentType := http.DetectContentType(head)
	if !slices.Contains(allowedTypes, contentType) {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedType, contentType)
	}

	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	hasher := sha256.New()
	// +1 so a stream exactly one byte over the cap is detected as over it.
	limited := io.LimitReader(io.MultiReader(bytes.NewReader(head), r), s.maxBytes+1)
	size, err := io.Copy(io.MultiWriter(tmp, hasher), limited)
	tmp.Close()
	if err != nil {
		return nil, err
	}
	if size > s.maxBytes {
		return nil, ErrTooLarge
	}

	hash := hex.EncodeToString(hasher.Sum(nil))
	meta := &Meta{Hash: hash, ContentType: contentType, Size: size}

	if err := s.writeMeta(meta); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpName, s.objectPath(hash)); err != nil {
		return nil, err
	}

	return meta, nil
}

// Open returns the object's metadata and a reader positioned at its start.
func (s *Store) Open(hash string) (*Meta, *os.File, error) {
	if !hashPattern.MatchString(hash) {
		return nil, nil, ErrNotFound
	}

	meta, err := s.readMeta(hash)
	if err != nil {
		return nil, nil, err
	}

	file, err := os.Open(s.objectPath(hash))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}

	return meta, file, nil
}

func (s *Store) objectPath(hash string) string {
	return filepath.Join(s.dir, hash)
}

func (s *Store) metaPath(hash string) string {
	return filepath.Join(s.dir, hash+".json")
}

func (s *Store) writeMeta(meta *Meta) error {
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(s.metaPath(meta.Hash), encoded, 0o644)
}

func (s *Store) readMeta(hash string) (*Meta, error) {
	raw, err := os.ReadFile(s.metaPath(hash))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	var meta Meta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}
