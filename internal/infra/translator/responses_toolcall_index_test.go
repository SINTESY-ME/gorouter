package translator

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// TestResponsesStreamInterleavedToolCallIndices reproduces an intermittent
// Codex "stops at the tool call" failure: when argument deltas for an
// already-opened tool call arrive after another tool call has taken over the
// active slot, the emit used the *current* output index instead of the
// item's own index. Codex keys tool execution on output_index, so a delta
// whose item_id and output_index disagree leaves the function_call item
// unfinished and the turn stalls at the tool call.
func TestResponsesStreamInterleavedToolCallIndices(t *testing.T) {
	input := strings.Join([]string{
		// tool call 0 opens
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"shell","arguments":"{\"cmd\":"}}]},"finish_reason":null}]}`,
		"",
		// tool call 1 opens -> closes tool 0, takes output_index 1
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]},"finish_reason":null}]}`,
		"",
		// late delta for tool call 0 (interleaved upstream chunking)
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")

	pr, pw := io.Pipe()
	go func() {
		err := streamOpenAIToResponses(context.Background(), newBufReader(strings.NewReader(input)), pw)
		_ = pw.CloseWithError(err)
	}()

	raw, _ := io.ReadAll(pr)
	out := string(raw)
	t.Logf("stream output:\n%s", out)

	// Walk the emitted events and verify every event that carries an item_id
	// also carries the output_index that item was opened with.
	openedAt := map[string]int{}
	type ev struct {
		Type      string `json:"type"`
		ItemID    string `json:"item_id"`
		OutputIdx *int   `json:"output_index"`
		Item      struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	for _, block := range strings.Split(out, "\n\n") {
		var dataLine string
		for _, ln := range strings.Split(block, "\n") {
			if strings.HasPrefix(ln, "data: ") {
				dataLine = strings.TrimPrefix(ln, "data: ")
			}
		}
		if dataLine == "" {
			continue
		}
		var e ev
		if err := json.Unmarshal([]byte(dataLine), &e); err != nil {
			continue
		}
		switch e.Type {
		case "response.output_item.added":
			if e.Item.ID != "" && e.OutputIdx != nil {
				openedAt[e.Item.ID] = *e.OutputIdx
			}
		case "response.function_call_arguments.delta", "response.function_call_arguments.done":
			if e.ItemID == "" || e.OutputIdx == nil {
				t.Errorf("%s missing item_id/output_index: %s", e.Type, dataLine)
				continue
			}
			want, ok := openedAt[e.ItemID]
			if !ok {
				t.Errorf("%s references item %s that was never opened", e.Type, e.ItemID)
				continue
			}
			if want != *e.OutputIdx {
				t.Errorf("%s for item %s emitted output_index=%d, but the item was opened at output_index=%d (mismatch stalls Codex at the tool call)",
					e.Type, e.ItemID, *e.OutputIdx, want)
			}
		}
	}
}
