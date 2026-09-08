package httpapi

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
)

const brandingBoundary = "brandingtestboundary"

// countingReader reports how much of a request body the handler actually
// consumed, which is the point of the bound: the upload has to be refused at
// the door rather than after it has been spooled to a temporary file.
type countingReader struct {
	reader io.Reader
	read   int64
}

func (c *countingReader) Read(buffer []byte) (int, error) {
	n, err := c.reader.Read(buffer)
	c.read += int64(n)
	return n, err
}

type repeatingReader struct{ value byte }

func (r repeatingReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = r.value
	}
	return len(buffer), nil
}

func multipartRequest(t *testing.T, body io.Reader) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/branding/logo", body)
	request.Header.Set("Content-Type", "multipart/form-data; boundary="+brandingBoundary)
	return request
}

func brandingPart(content []byte, contentType string) io.Reader {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.SetBoundary(brandingBoundary)
	headers := textproto.MIMEHeader{}
	headers.Set("Content-Disposition", `form-data; name="file"; filename="logo.png"`)
	headers.Set("Content-Type", contentType)
	part, _ := writer.CreatePart(headers)
	_, _ = part.Write(content)
	_ = writer.Close()
	return &buffer
}

func TestBrandingUploadRefusesAnOversizedBodyBeforeReadingItAll(t *testing.T) {
	prefix := "--" + brandingBoundary + "\r\n" +
		`Content-Disposition: form-data; name="file"; filename="logo.png"` + "\r\n" +
		"Content-Type: image/png\r\n\r\n"
	body := &countingReader{reader: io.MultiReader(
		strings.NewReader(prefix),
		io.LimitReader(repeatingReader{value: 'a'}, 64<<20),
		strings.NewReader("\r\n--"+brandingBoundary+"--\r\n"),
	)}
	w := httptest.NewRecorder()

	_, _, err := (&Server{}).readBrandingUpload(w, multipartRequest(t, body))

	apiError, ok := err.(*APIError)
	if !ok || apiError.Status != http.StatusUnprocessableEntity || apiError.Message != "이미지는 1MB 이하여야 합니다." {
		t.Fatalf("err = %#v, want the 1MB validation error", err)
	}
	if body.read > 4<<20 {
		t.Fatalf("read %d bytes of a 64MB body; the bound has to stop it near 1MB", body.read)
	}
}

func TestBrandingUploadKeepsAcceptingAnImageAtTheLimit(t *testing.T) {
	content := bytes.Repeat([]byte{'p'}, maxBrandingBytes)
	w := httptest.NewRecorder()

	stored, contentType, err := (&Server{}).readBrandingUpload(w, multipartRequest(t, brandingPart(content, "image/png")))
	if err != nil {
		t.Fatalf("readBrandingUpload: %v", err)
	}
	if contentType != "image/png" || len(stored) != maxBrandingBytes {
		t.Fatalf("contentType=%q size=%d", contentType, len(stored))
	}
}

func TestBrandingUploadRejectsANonImagePart(t *testing.T) {
	w := httptest.NewRecorder()

	_, _, err := (&Server{}).readBrandingUpload(w, multipartRequest(t, brandingPart([]byte("plain text"), "text/plain")))

	apiError, ok := err.(*APIError)
	if !ok || apiError.Status != http.StatusUnprocessableEntity {
		t.Fatalf("err = %#v, want a validation error", err)
	}
}
