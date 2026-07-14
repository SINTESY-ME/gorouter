// Package rtk implements Request Token Killer: content-aware compression
// of tool_result content in LLM request bodies. It auto-detects the type of
// tool output (git diff, grep, ls, etc.) and applies structural filters that
// reduce token count while preserving semantic meaning.
//
// The compressor is fail-open: any error (parse failure, filter panic, output
// growing) returns the original body unchanged. It is safe for concurrent use.
package rtk

import (
	"encoding/json"
	"log/slog"
	"unicode/utf8"
)

// Compression constants (mirrors Rust rtk defaults).
const (
	rawCap         = 10 * 1024 * 1024 // 10 MiB — skip bigger blobs
	minCompress    = 500               // bytes — skip tiny blobs
	detectWindow   = 1024              // autodetect peeks first N bytes
)

// Compressor implements domain.RequestCompressor using RTK filters.
// The zero value is ready to use. All state is in the package-level filter
// registry; the struct exists only to satisfy the interface.
type Compressor struct{}

// NewCompressor returns a ready RTK compressor.
func NewCompressor() *Compressor { return &Compressor{} }

// rtkStats tracks compression savings for logging.
type rtkStats struct {
	bytesBefore int
	bytesAfter  int
	hits        []rtkHit
}

type rtkHit struct {
	shape  string
	filter string
	saved  int
}

// Compress rewrites tool_result content in the JSON request body to reduce
// token count. Fail-open: on any error the original body is returned.
func (c *Compressor) Compress(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body // not JSON or malformed — pass through
	}
	stats := &rtkStats{}
	changed := compressMessages(raw, stats)
	if !changed {
		return body
	}
	out, err := json.Marshal(raw)
	if err != nil {
		return body
	}
	if len(out) >= len(body) {
		return body // never grow
	}
	if stats.bytesBefore > 0 && stats.bytesAfter < stats.bytesBefore {
		pct := float64(stats.bytesBefore-stats.bytesAfter) / float64(stats.bytesBefore) * 100
		filters := uniqueFilters(stats.hits)
		slog.Debug("rtk",
			"saved_bytes", stats.bytesBefore-stats.bytesAfter,
			"pct", pct,
			"filters", filters,
			"hits", len(stats.hits),
		)
	}
	return out
}

// compressMessages walks the parsed JSON body and compresses tool_result
// text content in-place. Returns true if any content was modified.
func compressMessages(body map[string]any, stats *rtkStats) bool {
	changed := false
	// OpenAI/Claude messages array OR OpenAI Responses input array.
	items, _ := body["messages"].([]any)
	if items == nil {
		items, _ = body["input"].([]any)
	}
	if items == nil {
		return false
	}
	for _, item := range items {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		// OpenAI Responses: { type:"function_call_output", output: string|[...] }
		if msg["type"] == "function_call_output" {
			changed = compressFunctionCallOutput(msg, stats) || changed
			continue
		}
		// OpenAI tool message: { role:"tool", content: string | [...] }
		if msg["role"] == "tool" {
			changed = compressToolContent(msg, stats, "openai-tool") || changed
			continue
		}
		// Claude tool_result block inside messages[].content[]
		content, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, block := range content {
			b, ok := block.(map[string]any)
			if !ok || b["type"] != "tool_result" {
				continue
			}
			if b["is_error"] == true {
				continue // preserve error traces
			}
			changed = compressToolResultBlock(b, stats) || changed
		}
	}
	return changed
}

// compressFunctionCallOutput compresses OpenAI Responses function_call_output.
func compressFunctionCallOutput(msg map[string]any, stats *rtkStats) bool {
	changed := false
	switch out := msg["output"].(type) {
	case string:
		compressed := compressText(out, stats, "responses-string")
		if compressed != out {
			msg["output"] = compressed
			changed = true
		}
	case []any:
		for _, part := range out {
			p, ok := part.(map[string]any)
			if !ok || p["type"] != "input_text" {
				continue
			}
			if text, ok := p["text"].(string); ok {
				compressed := compressText(text, stats, "responses-array")
				if compressed != text {
					p["text"] = compressed
					changed = true
				}
			}
		}
	}
	return changed
}

// compressToolContent compresses OpenAI { role:"tool", content } messages.
func compressToolContent(msg map[string]any, stats *rtkStats, shape string) bool {
	changed := false
	switch content := msg["content"].(type) {
	case string:
		compressed := compressText(content, stats, shape)
		if compressed != content {
			msg["content"] = compressed
			changed = true
		}
	case []any:
		for _, part := range content {
			p, ok := part.(map[string]any)
			if !ok || p["type"] != "text" {
				continue
			}
			if text, ok := p["text"].(string); ok {
				compressed := compressText(text, stats, shape+"-array")
				if compressed != text {
					p["text"] = compressed
					changed = true
				}
			}
		}
	}
	return changed
}

// compressToolResultBlock compresses Claude tool_result block content.
func compressToolResultBlock(block map[string]any, stats *rtkStats) bool {
	changed := false
	switch content := block["content"].(type) {
	case string:
		compressed := compressText(content, stats, "claude-string")
		if compressed != content {
			block["content"] = compressed
			changed = true
		}
	case []any:
		for _, part := range content {
			p, ok := part.(map[string]any)
			if !ok || p["type"] != "text" {
				continue
			}
			if text, ok := p["text"].(string); ok {
				compressed := compressText(text, stats, "claude-array")
				if compressed != text {
					p["text"] = compressed
					changed = true
				}
			}
		}
	}
	return changed
}

// compressText applies the auto-detected filter to a single text blob.
func compressText(text string, stats *rtkStats, shape string) string {
	n := len(text)
	stats.bytesBefore += n
	if n < minCompress || n > rawCap {
		stats.bytesAfter += n
		return text
	}
	fn := autoDetectFilter(text)
	if fn == nil {
		stats.bytesAfter += n
		return text
	}
	out := safeApply(fn, text)
	if len(out) == 0 || len(out) >= n {
		stats.bytesAfter += n
		return text
	}
	stats.bytesAfter += len(out)
	stats.hits = append(stats.hits, rtkHit{shape, fn.name, n - len(out)})
	return out
}

// floorCharBoundary avoids splitting a multi-byte UTF-8 character.
func floorCharBoundary(s string, end int) int {
	if end >= len(s) {
		return len(s)
	}
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return end
}

// uniqueFilters returns a deduplicated comma-joined list of filter names.
func uniqueFilters(hits []rtkHit) string {
	seen := map[string]bool{}
	var out []string
	for _, h := range hits {
		if !seen[h.filter] {
			seen[h.filter] = true
			out = append(out, h.filter)
		}
	}
	return joinStrings(out, ",")
}

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += sep + p
	}
	return out
}