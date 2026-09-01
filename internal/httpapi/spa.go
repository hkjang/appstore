package httpapi

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/hkjang/appstore/internal/webui"
)

type SPAHandler struct {
	dist fs.FS
}

func NewSPAHandler() (*SPAHandler, error) {
	dist, err := fs.Sub(webui.Dist, "dist")
	if err != nil {
		return nil, err
	}
	return &SPAHandler{dist: dist}, nil
}

func (s *SPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if clean == "." || clean == "" {
		clean = "index.html"
	}
	if info, err := fs.Stat(s.dist, clean); err == nil && !info.IsDir() {
		s.serveFile(w, r, clean)
		return
	}
	if path.Ext(clean) != "" || (!strings.Contains(r.Header.Get("Accept"), "text/html") && r.Header.Get("Accept") != "") {
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, r, "index.html")
}

func (s *SPAHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	value, err := fs.ReadFile(s.dist, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if name == "index.html" {
		contentType = "text/html; charset=utf-8"
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.Contains(name, ".") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Content-Length", fmtInt(len(value)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(value)
	}
}

func fmtInt(value int) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}
