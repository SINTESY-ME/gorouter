// Package sse provides thin helpers for piping Server-Sent Events between a
// client and an upstream. The encoder writes SSE chunks; the passthrough
// reader yields the upstream's body verbatim with minimal allocation.
//
// We do not parse and re-serialize each event — for the common case (same
// format on both sides, e.g. OpenAI client to OpenAI-compatible upstream)
// the body is forwarded byte-for-byte. Format translation, when required,
// is implemented by a Translator that wraps this reader.
package sse

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
)

// Headers are the standard SSE response headers with permissive CORS so
// browser-based clients (the dashboard) can connect on the same origin.
var Headers = map[string]string{
	"Content-Type":  "text/event-stream",
	"Cache-Control": "no-cache, no-transform",
	"Connection":    "keep-alive",
	"X-Accel-Buffering": "no",
}

// Write copies an upstream SSE body to w, flushing after each chunk so the
// client receives tokens as they arrive. It honours ctx cancellation so an
// aborted client request tears down the upstream connection promptly.
//
// Headers must already be written on w (status + Headers) before calling.
func Write(ctx context.Context, w http.ResponseWriter, body io.ReadCloser) error {
	defer body.Close()
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// WriteError emits a single SSE chunk carrying an OpenAI-style error object,
// then a [DONE] sentinel. Use when mid-stream failure must reach the client.
func WriteError(w http.ResponseWriter, status int, message string) {
	errObj := fmt.Sprintf(`{"error":{"message":%q,"type":"upstream_error","code":%d}}`, message, status)
	fmt.Fprintf(w, "data: %s\n\n", errObj)
	fmt.Fprintf(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// ParseEvent reads a single SSE event from r (terminated by a blank line).
// Returns the `data:` payload without the prefix, and io.EOF at end of
// stream. Used by translators that need to inspect/transform events.
func ParseEvent(r *bufio.Reader) (data string, done bool, err error) {
	for {
		line, e := r.ReadString('\n')
		if e != nil && e != io.EOF {
			return "", false, e
		}
		line = trimRight(line)
		if line == "" {
			if e == io.EOF {
				return "", true, nil
			}
			continue
		}
		if len(line) > 6 && line[:6] == "data: " {
			payload := line[6:]
			if payload == "[DONE]" {
				return "", true, nil
			}
			return payload, false, nil
		}
		if e == io.EOF {
			return "", true, nil
		}
	}
}

// ReadAll collects every remaining event's data payload. Intended for
// tests and small non-streaming-as-SSE responses; do not use on hot paths.
func ReadAll(r io.Reader) ([]string, error) {
	br := bufio.NewReader(r)
	var out []string
	for {
		data, done, err := ParseEvent(br)
		if err != nil {
			return out, err
		}
		if done {
			return out, nil
		}
		if data != "" {
			out = append(out, data)
		}
	}
}

func trimRight(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}