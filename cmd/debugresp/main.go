package main

import (
	"encoding/json"
	"fmt"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/translator"
)

func injectStreamUsage(body []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	so, _ := m["stream_options"].(map[string]any)
	if so == nil {
		so = map[string]any{}
	}
	so["include_usage"] = true
	m["stream_options"] = so
	b, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return b
}

func main() {
	tr := translator.New()
	for _, body := range [][]byte{
		[]byte(`{"model":"coding","input":[{"role":"user","content":"say hi"}],"stream":true}`),
		[]byte(`{"model":"coding","input":[{"role":"user","content":[{"type":"input_text","text":"say hi"}]}],"stream":true}`),
	} {
		fmt.Println("=== INPUT ===", string(body))
		step1, err := tr.TranslateRequest(domain.FormatResponses, domain.FormatOpenAI, "glm-5.2", body)
		fmt.Printf("step1 err=%v body=%s\n", err, step1)
		step2 := injectStreamUsage(step1)
		fmt.Printf("step2 body=%s\n", step2)
		step3, err := tr.TranslateRequest(domain.FormatOpenAI, domain.FormatOpenAI, "glm-5.2", step2)
		fmt.Printf("step3 err=%v body=%s\n\n", err, step3)
	}
}
