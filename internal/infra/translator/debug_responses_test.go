package translator

import (
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

func TestDebugResponsesToOpenAI(t *testing.T) {
	tr := New()
	cases := []string{
		`{"model":"coding","input":[{"role":"user","content":"say hi"}],"stream":true}`,
		`{"model":"coding","input":[{"role":"user","content":[{"type":"input_text","text":"say hi"}]}],"stream":true}`,
	}
	for _, body := range cases {
		out, err := tr.TranslateRequest(domain.FormatResponses, domain.FormatOpenAI, "glm-5.2", []byte(body))
		t.Logf("in=%s\nerr=%v\nout=%s\n", body, err, string(out))
	}
}
