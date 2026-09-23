// Command probe_cloudcode probes the Cloud Code Assist "fetchAvailableModels"
// endpoint with the OAuth connections stored in the gorouter database. It
// prints only field names, counts and model ids — never tokens.
//
// Usage (inside the repo, on the VPS):
//
//	GEMINI_REFRESH=<rt> ANTI_REFRESH=<rt> go run ./scripts/probe_cloudcode
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jhon/gorouter/internal/providers/oauth"
)

type target struct {
	name       string
	providerID string
	refresh    string
	userAgent  string
	project    string
}

func main() {
	targets := []target{
		{name: "gemini-cli", providerID: "gemini-cli", refresh: os.Getenv("GEMINI_REFRESH"), userAgent: "gemini-cli", project: os.Getenv("GEMINI_PROJECT")},
		{name: "antigravity", providerID: "antigravity", refresh: os.Getenv("ANTI_REFRESH"), userAgent: "antigravity/1.107.0 linux/amd64"},
	}
	mgr := oauth.NewManager()
	mgr.Register(&oauth.GeminiCLI{})
	mgr.Register(&oauth.Antigravity{})

	for _, tgt := range targets {
		if tgt.refresh == "" {
			fmt.Printf("%s: no refresh token in env, skipping\n", tgt.name)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		tok, err := mgr.Refresh(ctx, tgt.providerID, tgt.refresh)
		if err != nil {
			fmt.Printf("%s: refresh failed: %v\n", tgt.name, err)
			cancel()
			continue
		}
		fmt.Printf("=== %s: refreshed (expires_in=%d)\n", tgt.name, tok.ExpiresIn)
		if tgt.name == "gemini-cli" {
			probeGenAI(ctx, tok.AccessToken)
			for _, id := range []string{"gemini-2.5-flash", "gemini-2.5-pro", "gemini-3-flash-preview", "gemini-3-pro-preview", "gemini-3.1-pro-preview", "gemini-3.1-pro-high"} {
				probeGenerate(ctx, tgt, tok.AccessToken, id)
			}
		}
		for _, base := range []string{
			"https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels",
			"https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels",
		} {
			probeCloudCode(ctx, tgt, tok.AccessToken, base)
			if tgt.project != "" {
				probeCloudCodeProject(ctx, tgt, tok.AccessToken, base)
			}
		}
		cancel()
	}
}

// probeGenAI tries the public Generative Language API, which speaks the plain
// OpenAI-ish {"models":[...]} shape our parser already understands.
func probeGenAI(ctx context.Context, access string) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://generativelanguage.googleapis.com/v1beta/models?pageSize=200", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("  genai /v1beta/models: %v\n", err)
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	fmt.Printf("  genai /v1beta/models → %d (%d bytes)\n", resp.StatusCode, len(raw))
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("    body (first 300): %.300s\n", string(raw))
		return
	}
	describe(raw)
}

// probeGenerate answers the question the sync error cannot: is this connection
// able to chat at all, and which model ids does the account accept?
func probeGenerate(ctx context.Context, tgt target, access, model string) {
	wrap := map[string]any{
		"project": tgt.project,
		"model":   model,
		"request": map[string]any{
			"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "ping"}}}},
			"generationConfig": map[string]any{"maxOutputTokens": 1},
		},
	}
	body, _ := json.Marshal(wrap)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://cloudcode-pa.googleapis.com/v1internal:generateContent", bytes.NewReader(body))
	if err != nil {
		fmt.Printf("  generateContent %s: %v\n", model, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("User-Agent", tgt.userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("  generateContent %-26s → transport error: %v\n", model, err)
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	snippet := strings.Join(strings.Fields(string(raw)), " ")
	if len(snippet) > 160 {
		snippet = snippet[:160]
	}
	fmt.Printf("  generateContent %-26s → %d  %.160s\n", model, resp.StatusCode, snippet)
}

func probeCloudCode(ctx context.Context, tgt target, access, url string) {
	call(ctx, tgt, access, url, []byte(`{}`), true)
}

func probeCloudCodeProject(ctx context.Context, tgt target, access, url string) {
	body, _ := json.Marshal(map[string]any{"project": tgt.project})
	call(ctx, tgt, access, url, body, false)
}

func call(ctx context.Context, tgt target, access, url string, body []byte, describeBody bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fmt.Printf("  %s: request: %v\n", url, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("User-Agent", tgt.userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("  %s: %v\n", url, err)
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	resp.Body.Close()
	label := url
	if !describeBody {
		label += " (with project)"
	}
	fmt.Printf("  %s → %d (%d bytes)\n", label, resp.StatusCode, len(raw))
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("    body (first 200): %.200s\n", string(raw))
		return
	}
	if describeBody {
		describe(raw)
	}
}

// describe reports the response shape without assuming it, plus per-entry
// facts that decide which ids are really callable.
func describe(raw []byte) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		fmt.Printf("    unparseable: %v\n", err)
		return
	}
	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("    top[%q] = %T\n", k, top[k])
	}
	if arr, ok := top["models"].([]any); ok {
		fmt.Printf("    models is an ARRAY with %d entries\n", len(arr))
		for i, e := range arr {
			if i > 8 {
				break
			}
			fmt.Printf("      %d: %v\n", i, e)
		}
		return
	}
	m, ok := top["models"].(map[string]any)
	if !ok {
		return
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fmt.Printf("    models is a MAP with %d entries\n", len(ids))
	for _, id := range ids {
		entry, _ := m[id].(map[string]any)
		flags := []string{}
		if v, ok := entry["isInternal"].(bool); ok {
			flags = append(flags, fmt.Sprintf("isInternal=%v", v))
		}
		if v, ok := entry["apiProvider"].(string); ok {
			flags = append(flags, "apiProvider="+v)
		}
		if v, ok := entry["modelProvider"].(string); ok {
			flags = append(flags, "modelProvider="+v)
		}
		if v, ok := entry["model"].(string); ok {
			flags = append(flags, "model="+v)
		}
		if v, ok := entry["maxTokens"].(float64); ok {
			flags = append(flags, fmt.Sprintf("maxTokens=%.0f", v))
		}
		if _, ok := entry["quotaInfo"]; ok {
			flags = append(flags, "hasQuota")
		}
		fmt.Printf("      - %-34s %s\n", id, strings.Join(flags, " "))
	}
}
