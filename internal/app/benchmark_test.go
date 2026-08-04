package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/responsecache"
	"github.com/jhon/gorouter/internal/infra/rtk"
)

// benchBody returns a small chat request body with no tool results.
func benchBodySimple() []byte {
	return []byte(`{"model":"coding","messages":[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"Explain how HTTP caching works in one paragraph. @@NONCE@@"}]}`)
}

// benchBodyTools returns a chat request with a large tool_result blob so the
// RTK compressor actually matches a filter and re-encodes the body.
func benchBodyTools() []byte {
	// > minCompress (500 bytes) git-diff-shaped blob so autoDetect hits.
	diff := "diff --git a/internal/app/router.go b/internal/app/router.go\n" +
		"index 1234567..89abcde 100644\n" +
		"--- a/internal/app/router.go\n" +
		"+++ b/internal/app/router.go\n" +
		"@@ -1,20 +1,21 @@\n" +
		" package app\n" +
		" \n" +
		" import (\n" +
		"+\t\"log/slog\"\n" +
		" \t\"sync\"\n" +
		" )\n" +
		"@@ -10,7 +11,7 @@ type Foo struct {\n" +
		" \t// a comment that keeps going and going and going\n" +
		" \t// and going and going and going and going\n" +
		" \t// and going and going and going and going\n" +
		"-\tBar int\n" +
		"+\tBar int64\n" +
		" }\n" +
		"@@ -20,15 +21,15 @@ type Bar struct {\n" +
		" \tBaz string\n" +
		" \tQux float64\n" +
		" \tMore []byte\n" +
		" }\n"
	return []byte(`{"model":"coding","messages":[` +
		`{"role":"user","content":"apply this patch @@NONCE@@"},` +
		`{"role":"assistant","content":"","tool_calls":[]},` +
		`{"role":"tool","tool_call_id":"t1","content":` + fmt.Sprintf("%q", diff) + `}]}`)
}

// newBenchService builds a RouterService routing the "coding" combo (1 model,
// ordered_fallback) with the requested feature flags. Returns the service and
// a cleanup func.
func newBenchService(cacheOn, rtkOn, prewarm bool, body []byte) (*RouterService, func()) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`,
	}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{ID: "c1", ProviderID: "openai", Name: "test", IsActive: true}},
	}
	comboRepo := &mockComboRepo{combos: map[string]*domain.Combo{
		"coding": {Name: "coding", Models: []string{"openai/gpt-4o"}, Strategy: StrategyOrderedFallback},
	}}
	srv := NewRouterService(comboRepo, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})

	var closeFns []func()
	cleanup := func() {
		for _, f := range closeFns {
			f()
		}
	}

	if cacheOn {
		c := responsecache.NewMemory(10000, 5*time.Minute, time.Minute)
		closeFns = append(closeFns, c.Close)
		srv.Cache = NewCacheService(c)
		if prewarm {
			key := srv.Cache.ComputeKey(body, "coding", domain.FormatOpenAI)
			srv.Cache.Store(context.Background(), key, http.StatusOK, http.Header{}, []byte(`{"id":"cached","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`))
		}
	}
	if rtkOn {
		srv.Compressor = rtk.NewCompressor()
	}
	return srv, cleanup
}

func benchRouteChat(b *testing.B, body []byte, stream bool, cacheOn, rtkOn, prewarm, unique bool) {
	// Silence the payload slog line for the main matrix (Error level skips
	// arg evaluation) so we measure the router core. BenchmarkPayloadLog
	// isolates the logging cost explicitly.
	orig := slog.Default()
	lvl := new(slog.LevelVar)
	lvl.Set(slog.LevelError)
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: lvl})))
	defer slog.SetDefault(orig)

	srv, cleanup := newBenchService(cacheOn, rtkOn, prewarm, body)
	defer cleanup()

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reqBody := body
		if unique {
			// Vary the user content per iteration (valid JSON) so the
			// deterministic cache never hits — this measures the true cache
			// MISS path (key computation + routing + store) instead of
			// warming into a hit.
			reqBody = bytes.Replace(body, []byte("@@NONCE@@"), []byte(strconv.Itoa(i)), 1)
		}
		res, err := srv.RouteChat(ctx, reqBody, "coding", stream, "", RouteOptions{InputFormat: domain.FormatOpenAI})
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}
}

func BenchmarkRouteChat_NonStream(b *testing.B) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"simple", benchBodySimple()},
		{"tools", benchBodyTools()},
	} {
		for _, cfg := range []struct {
			name                    string
			cacheOn, rtkOn, prewarm bool
			unique                  bool
		}{
			{"baseline", false, false, false, false},
			{"cache_miss", true, false, false, true},
			{"cache_hit", true, false, true, false},
			{"rtk", false, true, false, false},
			{"both_miss", true, true, false, true},
			{"both_hit", true, true, true, false},
		} {
			b.Run(fmt.Sprintf("%s/%s", tc.name, cfg.name), func(b *testing.B) {
				benchRouteChat(b, tc.body, false, cfg.cacheOn, cfg.rtkOn, cfg.prewarm, cfg.unique)
			})
		}
	}
}

func BenchmarkRouteChat_Stream(b *testing.B) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"simple", benchBodySimple()},
		{"tools", benchBodyTools()},
	} {
		for _, cfg := range []struct {
			name                    string
			cacheOn, rtkOn, prewarm bool
			unique                  bool
		}{
			{"baseline", false, false, false, false},
			{"both_miss", true, true, false, true},
		} {
			b.Run(fmt.Sprintf("%s/%s", tc.name, cfg.name), func(b *testing.B) {
				benchRouteChat(b, tc.body, true, cfg.cacheOn, cfg.rtkOn, cfg.prewarm, cfg.unique)
			})
		}
	}
}

// BenchmarkPayloadLog isolates the cost of the "payload" slog line in
// executeOne (string(translated) + log emission). With slog at Error level the
// Info record is dropped before args are evaluated, so the delta is the log
// overhead. Global default logger is restored afterwards.
func BenchmarkPayloadLog(b *testing.B) {
	orig := slog.Default()
	defer slog.SetDefault(orig)

	bodies := []struct {
		name string
		body []byte
	}{
		{"simple", benchBodySimple()},
		{"tools", benchBodyTools()},
	}
	for _, tc := range bodies {
		for _, quiet := range []bool{false, true} {
			name := "logged"
			if quiet {
				name = "silent"
				lvl := new(slog.LevelVar)
				lvl.Set(slog.LevelError)
				slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: lvl})))
			} else {
				lvl := new(slog.LevelVar)
				lvl.Set(slog.LevelInfo)
				slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: lvl})))
			}
			b.Run(fmt.Sprintf("%s/%s", tc.name, name), func(b *testing.B) {
				benchRouteChat(b, tc.body, false, true, true, false, false)
			})
		}
	}
}
