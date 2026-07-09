package httpx

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jhon/gorouter/internal/app"
	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/sse"
)

// handleListModels returns the OpenAI-style model list (combos + connections'
// models, auto-fetched where possible).
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.Models.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := map[string]any{
		"object": "list",
		"data":   models,
	}
	writeJSON(w, http.StatusOK, out)
}

// handleChatWithFormat returns an http.HandlerFunc that handles chat-style
// requests in the given client input format (OpenAI, Anthropic, or Responses).
// The router translates the body to the upstream provider's format, executes,
// and translates the response back to the client format. stream is detected
// from the body and the response is piped through with minimal buffering.
func (s *Server) handleChatWithFormat(inputFormat domain.Format) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20)) // 16MB cap
		if err != nil {
			writeError(w, http.StatusBadRequest, "read body: "+err.Error())
			return
		}
		modelStr, stream, err := parseChatRequest(body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		apiKey := s.clientApiKey(r)
		res, err := s.Router.RouteChat(r.Context(), body, modelStr, stream, apiKey, app.RouteOptions{InputFormat: inputFormat})
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		defer res.Body.Close()
		for _, h := range []string{"Content-Type", "X-Request-Id"} {
			if v := res.Headers.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		if res.Stream {
			sseStreamResponse(w, r, res)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}
}

// parseChatRequest extracts the "model" and "stream" fields from an OpenAI
// chat request body in a single json.Unmarshal, avoiding a second parse in
// the router. For multipart bodies (audio/transcriptions) only the model is
// extracted via a multipart scan; stream defaults to false.
func parseChatRequest(body []byte) (model string, stream bool, err error) {
	var probe struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if jerr := json.Unmarshal(body, &probe); jerr == nil {
		if probe.Model == "" {
			return "", false, fmt.Errorf("model field is required")
		}
		return probe.Model, probe.Stream, nil
	}
	if m, ok := extractModelFromMultipartLocal(body); ok {
		return m, false, nil
	}
	return "", false, fmt.Errorf("could not parse request body")
}

// extractModelFromMultipartLocal is a thin wrapper for the multipart scan
// used when the body is not JSON. We avoid importing app internals here.
func extractModelFromMultipartLocal(body []byte) (string, bool) {
	const marker = `name="model"`
	idx := -1
	for i := 0; i < len(body)-len(marker); i++ {
		if string(body[i:i+len(marker)]) == marker {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", false
	}
	rest := body[idx+len(marker):]
	hdrEnd := -1
	for i := 0; i < len(rest)-4; i++ {
		if string(rest[i:i+4]) == "\r\n\r\n" {
			hdrEnd = i
			break
		}
	}
	if hdrEnd < 0 {
		return "", false
	}
	val := rest[hdrEnd+4:]
	end := len(val)
	for i := 0; i < len(val)-2; i++ {
		if string(val[i:i+2]) == "\r\n" {
			end = i
			break
		}
	}
	v := strings.TrimSpace(string(val[:end]))
	if v == "" {
		return "", false
	}
	return v, true
}

// sseStreamResponse writes SSE headers and pipes the upstream stream to the
// client via the sse package.
func sseStreamResponse(w http.ResponseWriter, r *http.Request, res *app.RouterResponse) {
	for k, v := range sse.Headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(http.StatusOK)
	if err := sse.Write(r.Context(), w, res.Body); err != nil {
		// Best-effort trailing error; the response is already partially
		// sent so we cannot change the status code.
		sse.WriteError(w, res.StatusCode, "upstream stream error")
	}
}

func (s *Server) handleNotImplemented(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "endpoint not implemented in this build")
}

// handlePassthrough routes a non-chat endpoint (embeddings, images) to the
// upstream. The body stays in OpenAI format; no translation, no streaming.
func (s *Server) handlePassthrough(endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 16 << 20))
		if err != nil {
			writeError(w, http.StatusBadRequest, "read body: "+err.Error())
			return
		}
		modelStr, _, perr := parseChatRequest(body)
		if perr != nil {
			// Non-JSON, non-multipart (shouldn't happen for these endpoints).
			modelStr = ""
		}
		apiKey := s.clientApiKey(r)
		res, err := s.Router.RoutePassthrough(r.Context(), body, modelStr, endpoint, apiKey, r.Header.Get("Content-Type"))
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		defer res.Body.Close()
		for _, h := range []string{"Content-Type", "X-Request-Id"} {
			if v := res.Headers.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}
}