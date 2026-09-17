package translator

// anthropicStopToOpenAI maps Anthropic's stop_reason to OpenAI's
// finish_reason. Unknown values become "stop" (the safe default).
func anthropicStopToOpenAI(s string) string {
	switch s {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "":
		return "stop"
	default:
		return "stop"
	}
}

// openAIToAnthropicStop is the inverse.
func openAIToAnthropicStop(s string) string {
	switch s {
	case "stop", "":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	case "content_filter":
		return "end_turn"
	default:
		return "end_turn"
	}
}