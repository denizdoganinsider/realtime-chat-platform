package controller

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"realtime-chat-platform/media-service/internal/storage"

	"github.com/labstack/echo/v4"
)

func pngBytes(size int) []byte {
	b := make([]byte, size)
	copy(b, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	return b
}

func newController(t *testing.T) *MediaController {
	t.Helper()
	store, err := storage.NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return NewMediaController(store)
}

func upload(t *testing.T, mc *MediaController, content []byte) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "avatar.png")
	_, _ = part.Write(content)
	writer.Close()

	e := echo.New()
	request := httptest.NewRequest(http.MethodPost, "/media", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	_ = mc.Upload(e.NewContext(request, recorder))

	return recorder
}

func get(mc *MediaController, hash string, ifNoneMatch string) *httptest.ResponseRecorder {
	e := echo.New()
	request := httptest.NewRequest(http.MethodGet, "/media/"+hash, nil)
	if ifNoneMatch != "" {
		request.Header.Set("If-None-Match", ifNoneMatch)
	}
	recorder := httptest.NewRecorder()
	c := e.NewContext(request, recorder)
	c.SetParamNames("hash")
	c.SetParamValues(hash)
	_ = mc.Get(c)

	return recorder
}

func TestUploadThenGetCarriesCDNHeaders(t *testing.T) {
	mc := newController(t)

	response := upload(t, mc, pngBytes(600))
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body=%s", response.Code, response.Body.String())
	}

	var created UploadResponse
	if err := decode(response, &created); err != nil {
		t.Fatal(err)
	}

	got := get(mc, created.Hash, "")
	if got.Code != http.StatusOK {
		t.Fatalf("GET status = %d", got.Code)
	}
	if got.Header().Get("ETag") != `"`+created.Hash+`"` {
		t.Errorf("ETag = %q, want the quoted hash", got.Header().Get("ETag"))
	}
	if cc := got.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if got.Header().Get("Content-Type") != "image/png" || got.Body.Len() != 600 {
		t.Errorf("body: type=%q len=%d", got.Header().Get("Content-Type"), got.Body.Len())
	}
}

func TestConditionalGetIs304WithoutDiskRead(t *testing.T) {
	mc := newController(t)

	// Never uploaded: a matching If-None-Match is still a 304, because the
	// URL alone says which bytes the client has.
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	response := get(mc, hash, `"`+hash+`"`)
	if response.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", response.Code)
	}
}

func TestGetUnknownIs404(t *testing.T) {
	mc := newController(t)
	if response := get(mc, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", ""); response.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.Code)
	}
}

func TestUploadRejectsNonImage(t *testing.T) {
	mc := newController(t)
	if response := upload(t, mc, []byte("<html>hi</html>")); response.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", response.Code)
	}
}
