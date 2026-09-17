package translator

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// The registry reads the pair key as the conversion direction for BOTH the
// request and the response: pair{from,to}.translateResponseJSON converts a
// response body that is in `from` format INTO `to` format. The router relies
// on that reading when it pivots responses through OpenAI:
//
//	step 3: TranslateResponseJSON(upstreamFmt, OpenAI, body)  // upstream -> pivot
//	step 4: TranslateResponseJSON(OpenAI, clientFmt, body)    // pivot -> client
//
// The tests below go through the registry exactly like the router does. The
// existing Impl-level tests call the converters directly, so an inverted
// registration stays green there — that is how /v1/messages ended up returning
// an OpenAI-shaped body to an Anthropic client.

const openAIResponseFixture = `{
  "id": "chatcmpl-1",
  "object": "chat.completion",
  "model": "deepseek/deepseek-v4.1-flash",
  "choices": [{"index": 0, "message": {"role": "assistant", "content": "olá mundo"}, "finish_reason": "stop"}],
  "usage": {"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}
}`

const anthropicResponseFixture = `{
  "id": "gen_01M2PB42QFHV9XFFJJEJZTVG6N",
  "type": "message",
  "role": "assistant",
  "model": "claude-x",
  "content": [{"type": "text", "text": "olá mundo"}],
  "stop_reason": "end_turn",
  "usage": {"input_tokens": 11, "output_tokens": 7}
}`

// OpenAI upstream -> Anthropic client. This is the direction /v1/messages
// needs: the client must receive content[], usage.input_tokens — never an
// OpenAI chat.completion body.
func TestRegistryResponseOpenAIToAnthropic(t *testing.T) {
	tr := New()
	out, err := tr.TranslateResponseJSON(domain.FormatOpenAI, domain.FormatAnthropic, []byte(openAIResponseFixture))
	if err != nil {
		t.Fatalf("translate openai->anthropic: %v", err)
	}

	var got struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not JSON: %v (%s)", err, out)
	}

	if got.Type != "message" {
		t.Errorf("type = %q, want %q (body: %s)", got.Type, "message", out)
	}
	if len(got.Content) != 1 || got.Content[0].Text != "olá mundo" {
		t.Errorf("content = %+v, want one text block with the upstream text", got.Content)
	}
	if got.Usage.InputTokens != 11 || got.Usage.OutputTokens != 7 {
		t.Errorf("usage = %+v, want input_tokens=11 output_tokens=7", got.Usage)
	}
	if got.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", got.StopReason)
	}
	if strings.Contains(string(out), `"chat.completion"`) {
		t.Errorf("Anthropic client must not receive an OpenAI object type: %s", out)
	}
}

// Anthropic upstream -> OpenAI client: the pivot body must carry the text and
// the token counts.
func TestRegistryResponseAnthropicToOpenAI(t *testing.T) {
	tr := New()
	out, err := tr.TranslateResponseJSON(domain.FormatAnthropic, domain.FormatOpenAI, []byte(anthropicResponseFixture))
	if err != nil {
		t.Fatalf("translate anthropic->openai: %v", err)
	}

	var got struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not JSON: %v (%s)", err, out)
	}

	if got.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", got.Object)
	}
	if len(got.Choices) != 1 || got.Choices[0].Message.Content != "olá mundo" {
		t.Errorf("choices = %+v, want the text preserved", got.Choices)
	}
	if got.Usage.PromptTokens != 11 || got.Usage.CompletionTokens != 7 {
		t.Errorf("usage = %+v, want prompt_tokens=11 completion_tokens=7", got.Usage)
	}
}

// Round trip both ways. If the two directions are served by the same
// converter (an inverted registration), the text is destroyed — this is the
// generic invariant, independent of the concrete field names.
func TestRegistryResponseRoundTripAnthropic(t *testing.T) {
	tr := New()

	viaAnthropic, err := tr.TranslateResponseJSON(domain.FormatOpenAI, domain.FormatAnthropic, []byte(openAIResponseFixture))
	if err != nil {
		t.Fatalf("openai->anthropic: %v", err)
	}
	back, err := tr.TranslateResponseJSON(domain.FormatAnthropic, domain.FormatOpenAI, viaAnthropic)
	if err != nil {
		t.Fatalf("anthropic->openai: %v", err)
	}
	if !strings.Contains(string(back), "olá mundo") {
		t.Errorf("openai->anthropic->openai dropped the text: %s", back)
	}

	viaOpenAI, err := tr.TranslateResponseJSON(domain.FormatAnthropic, domain.FormatOpenAI, []byte(anthropicResponseFixture))
	if err != nil {
		t.Fatalf("anthropic->openai: %v", err)
	}
	backToAnthropic, err := tr.TranslateResponseJSON(domain.FormatOpenAI, domain.FormatAnthropic, viaOpenAI)
	if err != nil {
		t.Fatalf("openai->anthropic: %v", err)
	}
	if !strings.Contains(string(backToAnthropic), "olá mundo") {
		t.Errorf("anthropic->openai->anthropic dropped the text: %s", backToAnthropic)
	}
}

const openAIStreamFixture = "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"olá\"}}]}\n\n" +
	"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" mundo\"},\"finish_reason\":\"stop\"}]}\n\n" +
	"data: [DONE]\n\n"

// Streaming direction: an Anthropic client must receive Anthropic SSE events,
// not OpenAI chat.completion.chunk frames.
func TestRegistryResponseStreamOpenAIToAnthropic(t *testing.T) {
	tr := New()
	out, err := tr.TranslateResponseStream(context.Background(), domain.FormatOpenAI, domain.FormatAnthropic,
		io.NopCloser(strings.NewReader(openAIStreamFixture)))
	if err != nil {
		t.Fatalf("translate stream openai->anthropic: %v", err)
	}
	defer out.Close()
	b, _ := io.ReadAll(out)
	body := string(b)

	if !strings.Contains(body, "event: message_start") {
		t.Errorf("missing Anthropic message_start event: %s", body)
	}
	if !strings.Contains(body, "content_block_delta") || !strings.Contains(body, "olá") {
		t.Errorf("missing Anthropic content_block_delta with the text: %s", body)
	}
	if strings.Contains(body, "chat.completion.chunk") {
		t.Errorf("Anthropic client received OpenAI SSE frames: %s", body)
	}
}

// Generic guard for every registered pair: a response converter must travel in
// the direction of its own key. The convention is encoded in the converter
// names (translate<From>To<To>…, <from>StreamTo<To>), so a converter whose
// name puts the key's `to` format before its `from` format is registered
// backwards. This is the check that was missing when the Anthropic pair was
// shipped inverted.
func TestRegistryResponseConvertersTravelInKeyDirection(t *testing.T) {
	tokens := map[domain.Format]string{
		domain.FormatOpenAI:    "openai",
		domain.FormatAnthropic: "anthropic",
		domain.FormatResponses: "responses",
		domain.FormatGemini:    "gemini",
	}

	for key, p := range defaultPairs {
		from, to := key[0], key[1]
		fromTok, okFrom := tokens[from]
		toTok, okTo := tokens[to]
		if !okFrom || !okTo {
			continue
		}
		converters := map[string]any{
			"translateResponseJSON":   p.translateResponseJSON,
			"translateResponseStream": p.translateResponseStream,
		}
		for label, fn := range converters {
			if fn == nil {
				continue
			}
			name := strings.ToLower(runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name())
			iFrom := strings.Index(name, fromTok)
			iTo := strings.Index(name, toTok)
			if iFrom < 0 || iTo < 0 {
				t.Errorf("%s->%s %s: %q does not name both formats", from, to, label, name)
				continue
			}
			if iFrom > iTo {
				t.Errorf("%s->%s %s is registered backwards: %q converts %s->%s instead",
					from, to, label, name, to, from)
			}
		}
	}
}
