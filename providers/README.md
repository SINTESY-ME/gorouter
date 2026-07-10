# Provider store

YAML definitions for pre-configured providers. The dashboard store lists files
from this directory on the main branch of the origin repository.

## Add a provider

1. Copy an existing YAML (e.g. `openai.yaml`) to `{id}.yaml`
2. Fill `id`, `display`, `transport`, `capabilities`
3. Open a PR

For protocols that are not OpenAI/Anthropic/Gemini/Responses compatible,
set `executor: your-id` and contribute a Go executor under
`internal/providers/executors/` (registered via `init()`).

## Schema

See `internal/providers/types.go` for the full `ProviderDef` shape.
