//go:build embed

// Package web embeds the built dashboard (web/dist). Build with:
//
//	go build -tags embed ./cmd/gorouter
//
// The build script writes web/dist via `npm run build` before invoking go.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

var indexHTML []byte

func init() {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("web: cannot access embedded dist: " + err.Error())
	}
	indexHTML, err = distFS.ReadFile("dist/index.html")
	if err != nil {
		panic("web: cannot read embedded index.html: " + err.Error())
	}
	Handler = spaHandler{fs: http.FileServer(http.FS(sub))}
}

// spaHandler serves real asset files when they exist; any other GET falls
// through to the embedded index.html so client-side routing works on
// refresh and deep links.
type spaHandler struct{ fs http.Handler }

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
		return
	}
	if _, err := fs.Stat(distFS, "dist/"+p); err == nil {
		h.fs.ServeHTTP(w, r)
		return
	}
	// SPA fallback: serve index.html directly (bypass FileServer which would
	// 301-redirect /index.html to /).
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}