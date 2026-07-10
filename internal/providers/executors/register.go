// Package executors holds optional specialized provider executors.
//
// The default HTTP executor handles OpenAI/Anthropic/Gemini/Responses.
// Custom protocols register here via Register and are selected by the
// YAML field `executor: <id>`.
package executors

import "github.com/jhon/gorouter/internal/domain"

// Factory builds a domain.Executor for a specialized provider.
type Factory func() domain.Executor

var factories = map[string]Factory{}

// Register binds an executor id to a factory. Call from init() in plugin files.
func Register(id string, f Factory) {
	if id == "" || f == nil {
		return
	}
	factories[id] = f
}

// Lookup returns a factory for id, or nil if the default HTTP executor should be used.
func Lookup(id string) Factory {
	return factories[id]
}
