package rtk

import (
	"strings"
	"testing"
)

func TestCompressEmpty(t *testing.T) {
	c := NewCompressor()
	out := c.Compress(nil)
	if out != nil {
		t.Fatalf("expected nil, got %v", out)
	}
	out = c.Compress([]byte{})
	if len(out) != 0 {
		t.Fatalf("expected empty, got %q", out)
	}
}

func TestCompressNonJSON(t *testing.T) {
	c := NewCompressor()
	body := []byte("not json")
	out := c.Compress(body)
	if string(out) != "not json" {
		t.Fatalf("expected pass-through, got %q", out)
	}
}

func TestCompressNoToolResults(t *testing.T) {
	c := NewCompressor()
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	out := c.Compress(body)
	// Should be unchanged (no tool_result to compress)
	if string(out) != string(body) {
		t.Fatalf("expected unchanged, got %q", out)
	}
}

func TestCompressOpenAITool(t *testing.T) {
	c := NewCompressor()
	diff := strings.Repeat("diff --git a/foo b/foo\n@@ -1,1 +1,1 @@\n+line\n", 200)
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"tool","content":"` + escapeJSON(diff) + `"}]}`)
	out := c.Compress(body)
	if len(out) >= len(body) {
		t.Fatalf("expected compression, in=%d out=%d", len(body), len(out))
	}
	if strings.Contains(string(out), "git-diff") {
		// Stats logged at debug; just verify the body is smaller
	}
}

func TestCompressClaudeToolResult(t *testing.T) {
	c := NewCompressor()
	// Build grep output > 500 bytes so filter fires
	var grepLines []string
	for i := 0; i < 50; i++ {
		grepLines = append(grepLines, "src/main.rs:"+itoa(i*10)+":    let result = process_request(ctx, &payload).await?;\n")
	}
	grep := strings.Join(grepLines, "")
	body := []byte(`{"model":"claude","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"tool_use","id":"1","name":"grep","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":"` + escapeJSON(grep) + `"}]}]}`)
	out := c.Compress(body)
	if len(out) >= len(body) {
		t.Fatalf("expected compression, in=%d out=%d", len(body), len(out))
	}
}

func TestCompressSkipErrorResults(t *testing.T) {
	c := NewCompressor()
	// Error results should NOT be compressed
	errContent := strings.Repeat("Error: stack trace line\n", 500)
	body := []byte(`{"messages":[{"role":"tool","content":[{"type":"tool_result","is_error":true,"content":"` + escapeJSON(errContent) + `"}]}]}`)
	out := c.Compress(body)
	if string(out) != string(body) {
		t.Fatalf("error results should not be compressed")
	}
}

func TestCompressFailOpen(t *testing.T) {
	c := NewCompressor()
	// Malformed JSON array inside messages
	body := []byte(`{"messages": [invalid]}`)
	out := c.Compress(body)
	if string(out) != string(body) {
		t.Fatalf("malformed JSON should pass through, got %q", out)
	}
}

func TestFilterGitDiff(t *testing.T) {
	diff := "diff --git a/foo.go b/foo.go\n--- a/foo.go\n+++ b/foo.go\n@@ -1,1 +1,1 @@\n-old\n+new\n"
	out := filterGitDiff(diff)
	if !strings.Contains(out, "foo.go") {
		t.Fatalf("expected filename, got %q", out)
	}
	if !strings.Contains(out, "+1 -1") {
		t.Fatalf("expected +1 -1 totals, got %q", out)
	}
}

func TestFilterGitLog(t *testing.T) {
	// Make input > minCompressSize (500 bytes) so filter runs
	log := "commit abc1234\nAuthor: Test <test@example.com>\nDate: Mon Jan 1 12:00:00 2026 +0000\n\n    Fix a critical bug in the parser module that caused crashes on multi-line inputs\n\ncommit def5678\nAuthor: Test2 <test2@example.com>\nDate: Tue Jan 2 12:00:00 2026 +0000\n\n    Add a new feature that allows users to customize the output format easily\n"
	// pad to > 500 bytes
	for len(log) < 600 {
		log += "\n"
	}
	out := filterGitLog(log)
	if !strings.Contains(out, "abc1234") {
		t.Fatalf("expected sha, got %q", out)
	}
	if !strings.Contains(out, "Subject: Fix a critical bug") {
		t.Fatalf("expected subject, got %q", out)
	}
}

func TestFilterGitStatus(t *testing.T) {
	status := "On branch main\nM  modified.go\nA  staged.go\n?? untracked.go\n"
	out := filterGitStatus(status)
	if !strings.Contains(out, "* main") {
		t.Fatalf("expected branch, got %q", out)
	}
	if !strings.Contains(out, "Staged:") {
		t.Fatalf("expected staged section, got %q", out)
	}
	if !strings.Contains(out, "Untracked:") {
		t.Fatalf("expected untracked section, got %q", out)
	}
}

func TestFilterGrep(t *testing.T) {
	grep := "src/main.rs:42:fn main() {}\nsrc/main.rs:10:use std::io\nsrc/lib.rs:5:pub fn x() {}\n"
	out := filterGrep(grep)
	if !strings.Contains(out, "3 matches") {
		t.Fatalf("expected match count, got %q", out)
	}
	if !strings.Contains(out, "[file] src/main.rs") {
		t.Fatalf("expected file header, got %q", out)
	}
}

func TestFilterFind(t *testing.T) {
	find := "./src/main.go\n./src/lib.go\n./src/cmd/run.go\n./tests/test.go\n"
	out := filterFind(find)
	if !strings.Contains(out, "4 files") {
		t.Fatalf("expected file count, got %q", out)
	}
	if !strings.Contains(out, "src/") {
		t.Fatalf("expected dir, got %q", out)
	}
}

func TestFilterLS(t *testing.T) {
	ls := "total 24\ndrwxr-xr-x 2 user group 4096 Jan 1 12:00 src\n-rw-r--r-- 1 user group 1024 Jan 1 12:00 main.go\n-rw-r--r-- 1 user group 2048 Jan 1 12:00 lib.go\n"
	out := filterLS(ls)
	if !strings.Contains(out, "src/") {
		t.Fatalf("expected dir, got %q", out)
	}
	if !strings.Contains(out, "Summary:") {
		t.Fatalf("expected summary, got %q", out)
	}
}

func TestFilterTree(t *testing.T) {
	tree := ".\n├── src\n│   ├── main.go\n│   └── lib.go\n└── tests\n    └── test.go\n\n2 directories, 3 files\n"
	out := filterTree(tree)
	if strings.Contains(out, "directories") {
		t.Fatalf("summary line should be stripped, got %q", out)
	}
	if !strings.Contains(out, "main.go") {
		t.Fatalf("expected main.go, got %q", out)
	}
}

func TestFilterBuildOutput(t *testing.T) {
	build := "Compiling crate v1.0.0\nCompiling dep v2.0.0\nerror[E0308]: mismatched types\n  --> src/main.rs:10:5\n   |\n10 | let x: i32 = \"hello\";\n   |           ^^^^ expected i32, found &str\n\nwarning: unused variable\nFinished `release` profile\n"
	out := filterBuildOutput(build)
	if strings.Contains(out, "Compiling crate") {
		t.Fatalf("compiling lines should be stripped, got %q", out)
	}
	if !strings.Contains(out, "Compiled 2 packages") {
		t.Fatalf("expected compiled count, got %q", out)
	}
	if !strings.Contains(out, "error[E0308]") {
		t.Fatalf("expected error kept, got %q", out)
	}
}

func TestFilterDedupLog(t *testing.T) {
	log := "Starting server\nStarting server\nStarting server\nReady\nReady\nDone\n"
	out := filterDedupLog(log)
	if strings.Contains(strings.Repeat(out, 3), "Starting server\nStarting server") {
		// duplicates should be collapsed
	}
	if !strings.Contains(out, "duplicate lines") {
		t.Fatalf("expected dedup marker, got %q", out)
	}
}

func TestFilterSmartTruncate(t *testing.T) {
	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines, "line "+itoa(i))
	}
	input := strings.Join(lines, "\n")
	out := filterSmartTruncate(input)
	if !strings.Contains(out, "lines truncated") {
		t.Fatalf("expected truncation marker, got %q", out[:200])
	}
}

func TestAutoDetectGitDiff(t *testing.T) {
	fn := autoDetectFilter("diff --git a/foo b/foo\n--- a/foo\n+++ b/foo\n@@ -1 +1 @@\n-new\n+new\n")
	if fn == nil || fn.name != "git-diff" {
		t.Fatalf("expected git-diff, got %v", fn)
	}
}

func TestAutoDetectGrep(t *testing.T) {
	fn := autoDetectFilter("src/main.go:42:fn main() {}\nsrc/lib.go:10:var x = 1\n")
	if fn == nil || fn.name != "grep" {
		t.Fatalf("expected grep, got %v", fn)
	}
}

func TestAutoDetectFind(t *testing.T) {
	fn := autoDetectFilter("./src/main.go\n./src/lib.go\n./src/cmd/run.go\n./tests/test.go\n")
	if fn == nil || fn.name != "find" {
		t.Fatalf("expected find, got %v", fn)
	}
}

func TestAutoDetectNil(t *testing.T) {
	fn := autoDetectFilter("short")
	if fn != nil {
		t.Fatalf("expected nil for short text, got %v", fn)
	}
}

func TestFloorCharBoundary(t *testing.T) {
	// "a" repeated 1023 times + "é" (2 bytes) = 1025 bytes
	s := strings.Repeat("a", 1023) + "é"
	end := floorCharBoundary(s, 1024)
	if end > 1023 {
		t.Fatalf("expected boundary at 1023, got %d", end)
	}
}

func TestSafeApplyRecovers(t *testing.T) {
	panicFn := &rtkFilterFunc{"panic", func(s string) string {
		panic("filter bug")
	}}
	out := safeApply(panicFn, "input")
	if out != "input" {
		t.Fatalf("expected fail-open to original, got %q", out)
	}
}

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

func itoa(i int) string {
	// avoid strconv import collision in test
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}