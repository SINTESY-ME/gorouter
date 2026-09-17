package app

import (
	"strings"

	"github.com/jhon/gorouter/internal/domain"
)

// The metadata chain has one rule: the closer to the model, the more it knows.
// A provider's own /models response is the first link because that provider
// runs the model; the external registries (LiteLLM, models.dev, OpenRouter) are
// looked up only for what the provider did not state. Nothing here invents a
// value: an absent field stays zero, and the chain decides what to do with it.
//
// There is no single shape to read. Providers answer with a flat
// context_length, a nested token_limits object, or metadata carrying its own
// limits, so each field is looked for in the places that actually occur rather
// than assuming one layout.

// providerModelMetadata extracts everything the provider itself stated about a
// model it returned in its own model list.
func providerModelMetadata(m map[string]any) domain.ModelMetadata {
	reasoning := providerReasoning(m)
	return domain.ModelMetadata{
		Context:           providerContext(m),
		MaxOutputTokens:   providerMaxOutput(m),
		SupportsVision:    providerSupportsVision(m),
		SupportsToolCall:  providerSupportsToolCall(m),
		SupportsReasoning: reasoning.SupportsReasoning,
		Reasoning:         reasoning,
	}
}

// providerContext reads the input window. Shapes seen in the wild:
// context_length (top level), token_limits.context_window, metadata.
// context_length, and limit.context.
func providerContext(m map[string]any) int {
	if n := firstPositiveInt(m, "context_length", "context_window", "max_context_length", "max_model_len", "context"); n > 0 {
		return n
	}
	if n := firstPositiveInt(obj(m, "token_limits"), "context_window", "context_length"); n > 0 {
		return n
	}
	if n := firstPositiveInt(obj(m, "metadata"), "context_length", "context_window"); n > 0 {
		return n
	}
	return firstPositiveInt(obj(m, "limit"), "context", "context_length")
}

// providerMaxOutput reads the response ceiling, which is a different fact from
// the window: a model can accept a million tokens and still answer with 16k.
// max_tokens is read last because some providers use it for the window.
func providerMaxOutput(m map[string]any) int {
	if n := firstPositiveInt(m, "max_output_tokens", "max_completion_tokens"); n > 0 {
		return n
	}
	if n := firstPositiveInt(obj(m, "top_provider"), "max_completion_tokens", "max_output_tokens"); n > 0 {
		return n
	}
	if n := firstPositiveInt(obj(m, "token_limits"), "max_output_token_length", "max_completion_tokens"); n > 0 {
		return n
	}
	if n := firstPositiveInt(obj(m, "metadata"), "max_tokens", "max_output_tokens", "max_completion_tokens"); n > 0 {
		return n
	}
	return firstPositiveInt(m, "max_tokens")
}

// providerSupportsVision looks for an image input modality, as a modality list
// or as the older single-modality string.
func providerSupportsVision(m map[string]any) bool {
	if truthy(m["supports_vision"]) {
		return true
	}
	for _, holder := range []map[string]any{m, obj(m, "architecture"), obj(m, "modalities")} {
		if holder == nil {
			continue
		}
		if listsString(holder["input_modalities"], "image") || listsString(holder["modalities"], "image") {
			return true
		}
		if s, ok := holder["modality"].(string); ok && strings.Contains(strings.ToLower(s), "image") {
			return true
		}
	}
	return false
}

// providerSupportsToolCall reads a capability flag, or the tool entry in the
// list of parameters the model accepts.
func providerSupportsToolCall(m map[string]any) bool {
	if truthy(m["supports_function_calling"]) || truthy(m["supports_tool_call"]) {
		return true
	}
	return hasParam(m, "tools") || hasParam(m, "tool_choice")
}

// providerReasoning reads what the provider stated about reasoning. The
// richest shape is an object that carries the ladder itself — OpenRouter
// publishes {"mandatory":false,"supported_efforts":[...],"default_effort":...}
// in its own /models — which makes efforts a first-class part of the chain
// rather than something only the external registries know.
func providerReasoning(m map[string]any) domain.ReasoningCapabilities {
	caps := domain.ReasoningCapabilities{}
	if r := obj(m, "reasoning"); r != nil {
		caps.SupportsReasoning = true
		if ladder := stringList(r["supported_efforts"]); len(ladder) > 0 {
			caps.Efforts, caps.EffortsStated = domain.SortEfforts(ladder), true
		}
		if d, ok := r["default_effort"].(string); ok {
			caps.DefaultEffort = strings.ToLower(strings.TrimSpace(d))
		}
		if b, ok := r["mandatory"].(bool); ok {
			caps.Mandatory = b
		}
	}
	if ladder := reasoningLadderFromOptions(m["reasoning_options"]); len(ladder) > 0 {
		caps.Efforts, caps.EffortsStated = domain.SortEfforts(ladder), true
	}
	// A bare flag, a dedicated reasoning budget, or the reasoning entry in the
	// list of parameters the model accepts.
	if truthy(m["supports_reasoning"]) || truthy(m["reasoning"]) || len(caps.Efforts) > 0 {
		caps.SupportsReasoning = true
	}
	if firstPositiveInt(obj(m, "token_limits"), "max_reasoning_token_length") > 0 {
		caps.SupportsReasoning = true
	}
	if hasParam(m, "reasoning") || hasParam(m, "reasoning_effort") || hasParam(m, "include_reasoning") {
		caps.SupportsReasoning = true
	}
	if len(caps.Efforts) == 0 && reasoningCapsStated(caps) {
		caps.Efforts = ladderFromFlags(caps)
	}
	return caps
}

// reasoningLadderFromOptions reads a reasoning_options list, the shape
// models.dev publishes: only the entry whose type is "effort" carries levels.
// The other types ("toggle", "budget_tokens") describe a different control
// surface and are not turned into a ladder.
func reasoningLadderFromOptions(v any) []string {
	options, ok := v.([]any)
	if !ok {
		return nil
	}
	var ladder []string
	for _, raw := range options {
		option, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if kind, _ := option["type"].(string); !strings.EqualFold(kind, "effort") {
			continue
		}
		ladder = append(ladder, stringList(option["values"])...)
	}
	return ladder
}

// providerSupportsReasoning reads a capability flag, a reasoning object, a
// dedicated reasoning budget, or the reasoning entry in the accepted
// parameters.
func providerSupportsReasoning(m map[string]any) bool {
	return providerReasoning(m).SupportsReasoning
}

// obj returns a nested JSON object, or nil when the key is absent or is not an
// object.
func obj(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	nested, _ := m[key].(map[string]any)
	return nested
}

// firstPositiveInt returns the first of the named keys holding a positive
// number. Anything else (absent, zero, negative, non-numeric) means the source
// did not state the field.
func firstPositiveInt(m map[string]any, keys ...string) int {
	if m == nil {
		return 0
	}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if n := int(floatVal(v)); n > 0 {
				return n
			}
		}
	}
	return 0
}

// truthy reports an explicit true, treating any other value as "not stated".
func truthy(v any) bool {
	b, _ := v.(bool)
	return b
}

// listsString reports whether a JSON array holds the given string, ignoring
// case (modality names appear as "image" and "IMAGE").
func listsString(v any, target string) bool {
	list, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range list {
		if s, ok := item.(string); ok && strings.EqualFold(s, target) {
			return true
		}
	}
	return false
}
