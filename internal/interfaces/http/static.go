package httpx

import (
	"net/http"
)

// staticAssetsHandler is set by internal/web via SetStaticHandler at boot.
// When nil, dashboard GETs return 404 (dev mode: frontend served by Vite).
var staticAssetsHandler http.Handler

// SetStaticHandler wires an http.Handler that serves the embedded dashboard
// build (SPA fallback to index.html). Called once at startup.
func SetStaticHandler(h http.Handler) { staticAssetsHandler = h }

// staticHandler serves the embedded dashboard. It is the catch-all for non
// API, non /v1 GET requests. When no assets are embedded, it 404s.
func staticHandler(w http.ResponseWriter, r *http.Request) {
	if staticAssetsHandler == nil {
		http.NotFound(w, r)
		return
	}
	staticAssetsHandler.ServeHTTP(w, r)
}