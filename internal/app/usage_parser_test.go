package app

import (
	"encoding/json"
	"testing"
)

func TestParseUsageFromJSON(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		prompt int
		compl  int
	}{
		{"with usage", `{"id":"x","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`, 10, 20},
		{"no usage", `{"id":"x","choices":[]}`, 0, 0},
		{"null usage", `{"id":"x","choices":[],"usage":null}`, 0, 0},
		{"invalid json", `{not json}`, 0, 0},
		{"empty", ``, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, c := parseUsageFromJSON([]byte(tt.body))
			if p != tt.prompt || c != tt.compl {
				t.Errorf("got (%d,%d) want (%d,%d)", p, c, tt.prompt, tt.compl)
			}
		})
	}
}

func TestParseUsageFromSSE(t *testing.T) {
	sse := "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"there\"}}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":10}}\n\n" +
		"data: [DONE]\n\n"

	p, c := parseUsageFromSSE([]byte(sse))
	if p != 5 || c != 10 {
		t.Errorf("got (%d,%d) want (5,10)", p, c)
	}
}

func TestParseUsageFromSSE_NoUsage(t *testing.T) {
	sse := "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: [DONE]\n\n"

	p, c := parseUsageFromSSE([]byte(sse))
	if p != 0 || c != 0 {
		t.Errorf("got (%d,%d) want (0,0)", p, c)
	}
}

func TestInjectStreamUsage(t *testing.T) {
	t.Run("adds to body without stream_options", func(t *testing.T) {
		body := `{"model":"gpt-4","stream":true,"messages":[]}`
		out := injectStreamUsage([]byte(body))
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		so, ok := m["stream_options"].(map[string]any)
		if !ok {
			t.Fatal("stream_options not set")
		}
		if so["include_usage"] != true {
			t.Fatal("include_usage not true")
		}
	})
	t.Run("preserves existing stream_options", func(t *testing.T) {
		body := `{"model":"gpt-4","stream":true,"stream_options":{"foo":"bar"}}`
		out := injectStreamUsage([]byte(body))
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		so := m["stream_options"].(map[string]any)
		if so["foo"] != "bar" {
			t.Fatal("existing option lost")
		}
		if so["include_usage"] != true {
			t.Fatal("include_usage not added")
		}
	})
	t.Run("invalid json returns original", func(t *testing.T) {
		body := `not json`
		out := injectStreamUsage([]byte(body))
		if string(out) != body {
			t.Fatal("should return original on invalid json")
		}
	})
}
