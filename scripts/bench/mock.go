// Command mockbench is the minimal OpenAI-compatible upstream used by the
// proxy benchmark (docs/BENCHMARK.md). It answers /v1/models (probe) and
// /v1/chat/completions (requests) with fixed payloads — no logging, no auth,
// no disk I/O — so the latency floor of the stack is fixed.
package main

import (
	"log"
	"net/http"
)

const chatBody = `{"id":"mock-1","object":"chat.completion","created":0,"model":"mock","choices":[{"index":0,"message":{"role":"assistant","content":"hello from mock"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`

const modelsBody = `{"object":"list","data":[{"id":"mock","object":"model","owned_by":"mock"}]}`

func main() {
	http.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(modelsBody))
	})
	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(chatBody))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(chatBody))
	})
	log.Fatal(http.ListenAndServe("127.0.0.1:19998", nil))
}
