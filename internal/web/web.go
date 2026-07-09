// Package web embeds the built dashboard (web/dist) so the final binary
// serves its own UI with no external files. When the build is absent
// (development, frontend served by Vite), the no-embed handler returns 404
// for dashboard GETs and the API still works.
//
// Embedding uses a build tag: by default we ship no assets (so the embed
// directive compiles without a dist/ folder). A second file, web_embedded.go,
// has the `embed` build tag and is selected via `-tags embed` (set by the
// production build script).
package web

import "net/http"

// Handler serves the dashboard UI. By default it 404s. When built with
// -tags embed, the embedding file overrides this var with an http.FileServer
// over the embedded FS.
var Handler http.Handler = http.NotFoundHandler()