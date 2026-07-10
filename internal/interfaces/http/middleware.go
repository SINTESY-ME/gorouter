package httpx

import (
	"log/slog"
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// corsMiddleware allows any origin so browser-based clients (including the
// embedded dashboard, served on the same origin) can call /v1. Locking this
// down per-host is a v2 concern.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, x-api-key, anthropic-version")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// zapLogger logs only non-2xx responses to keep the hot path quiet on
// success. Usage tracking (tokens, latency, TPS) is handled separately by
// the AsyncUsageRecorder, so skipping success logs doesn't affect the
// dashboard Logs tab.
func zapLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if ww.Status() >= 400 {
			slog.Warn("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"ms", time.Since(start).Milliseconds(),
				"req_id", chimw.GetReqID(r.Context()),
			)
		}
	})
}