package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrniemiec/c2/core"
)

// newTestStore creates a FileStore backed by a temp directory.
func newTestStore(t *testing.T) *FileStore {
	t.Helper()
	dir := t.TempDir()
	return New(dir)
}

// --- sanitize ---

func TestSanitize(t *testing.T) {
	ok := []string{"foo", "foo/bar", "a-b_c"}
	for _, name := range ok {
		if _, err := sanitize(name); err != nil {
			t.Errorf("sanitize(%q) unexpected error: %v", name, err)
		}
	}

	bad := []string{"", "  ", "..", "../etc", "/absolute"}
	for _, name := range bad {
		if _, err := sanitize(name); err == nil {
			t.Errorf("sanitize(%q) expected error, got nil", name)
		}
	}
}

// --- ListTopics ---

func TestListTopics_Empty(t *testing.T) {
	st := newTestStore(t)
	topics, err := st.ListTopics()
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 0 {
		t.Fatalf("expected 0 topics, got %d", len(topics))
	}
}

func TestListTopics(t *testing.T) {
	st := newTestStore(t)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := st.SaveSystem(name, "sys"); err != nil {
			t.Fatal(err)
		}
	}
	topics, err := st.ListTopics()
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 3 {
		t.Fatalf("expected 3 topics, got %d: %v", len(topics), topics)
	}
	// Should be sorted.
	if topics[0] != "alpha" || topics[1] != "beta" || topics[2] != "gamma" {
		t.Errorf("unexpected order: %v", topics)
	}
}

// --- System prompt ---

func TestSaveLoadSystem(t *testing.T) {
	st := newTestStore(t)
	const text = "You are a helpful assistant."

	if err := st.SaveSystem("mytopic", text); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadSystem("mytopic")
	if err != nil {
		t.Fatal(err)
	}
	if got != text {
		t.Errorf("got %q, want %q", got, text)
	}
}

func TestLoadSystem_Missing(t *testing.T) {
	st := newTestStore(t)
	got, err := st.LoadSystem("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("expected empty string for missing system, got %q", got)
	}
}

// --- History ---

func TestSaveLoadHistory(t *testing.T) {
	st := newTestStore(t)
	h := core.NewHistory()
	h.Append(core.RoleUser, "hello")
	h.Append(core.RoleAssistant, "hi there")

	if err := st.SaveHistory("t1", h); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadHistory("t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded.Msgs))
	}
	if loaded.Msgs[0].Content != "hello" || loaded.Msgs[1].Content != "hi there" {
		t.Errorf("unexpected messages: %+v", loaded.Msgs)
	}
}

func TestLoadHistory_Missing(t *testing.T) {
	st := newTestStore(t)
	h, err := st.LoadHistory("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Msgs) != 0 {
		t.Errorf("expected empty history, got %d messages", len(h.Msgs))
	}
}

func TestClearHistory(t *testing.T) {
	st := newTestStore(t)
	h := core.NewHistory()
	h.Append(core.RoleUser, "question")
	h.Append(core.RoleAssistant, "answer")
	if err := st.SaveHistory("t1", h); err != nil {
		t.Fatal(err)
	}
	// Also save a summary that should be removed.
	if err := st.SaveSummary("t1", "some summary", 1); err != nil {
		t.Fatal(err)
	}

	if err := st.ClearHistory("t1"); err != nil {
		t.Fatal(err)
	}
	loaded, _ := st.LoadHistory("t1")
	if len(loaded.Msgs) != 0 {
		t.Errorf("expected empty history after clear, got %d messages", len(loaded.Msgs))
	}
	text, idx, _ := st.LoadSummary("t1")
	if text != "" || idx != -1 {
		t.Errorf("expected summary to be removed after clear, got %q/%d", text, idx)
	}
}

// --- Summary ---

func TestSaveLoadSummary(t *testing.T) {
	st := newTestStore(t)
	const body = "This is the summary text."
	if err := st.SaveSummary("t1", body, 5); err != nil {
		t.Fatal(err)
	}
	text, idx, err := st.LoadSummary("t1")
	if err != nil {
		t.Fatal(err)
	}
	if text != body {
		t.Errorf("got %q, want %q", text, body)
	}
	if idx != 5 {
		t.Errorf("got coversThrough=%d, want 5", idx)
	}
}

func TestLoadSummary_Missing(t *testing.T) {
	st := newTestStore(t)
	text, idx, err := st.LoadSummary("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if text != "" || idx != -1 {
		t.Errorf("expected (\"\", -1, nil) for missing summary, got (%q, %d)", text, idx)
	}
}

// --- parseSummaryFile ---

func TestParseSummaryFile(t *testing.T) {
	input := "covers_through: 42\n---\nhello\nworld"
	text, idx, err := parseSummaryFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if idx != 42 {
		t.Errorf("coversThrough = %d, want 42", idx)
	}
	if text != "hello\nworld" {
		t.Errorf("body = %q, want %q", text, "hello\nworld")
	}
}

func TestParseSummaryFile_Invalid(t *testing.T) {
	cases := []string{
		"no separator here",
		"wrong_header: 1\n---\nbody",
		"covers_through: abc\n---\nbody",
	}
	for _, c := range cases {
		if _, _, err := parseSummaryFile(c); err == nil {
			t.Errorf("parseSummaryFile(%q) expected error, got nil", c)
		}
	}
}

// --- DeleteTopic ---

func TestDeleteTopic(t *testing.T) {
	st := newTestStore(t)
	if err := st.SaveSystem("tobedeleted", "sys"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTopic("tobedeleted"); err != nil {
		t.Fatal(err)
	}
	topics, _ := st.ListTopics()
	for _, tp := range topics {
		if tp == "tobedeleted" {
			t.Error("topic still present after delete")
		}
	}
}

// --- CreateResource ---

func TestCreateResource(t *testing.T) {
	st := newTestStore(t)

	// Write a temp source file.
	src := filepath.Join(t.TempDir(), "data.txt")
	if err := os.WriteFile(src, []byte("resource content"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := st.CreateResource("t1", src); err != nil {
		t.Fatal(err)
	}

	// Verify the file landed in resources/.
	dir, _ := st.topicDir("t1")
	dst := filepath.Join(dir, "resources", "data.txt")
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("resource file not found: %v", err)
	}
	if string(b) != "resource content" {
		t.Errorf("unexpected content: %q", string(b))
	}

	// Duplicate should fail.
	if err := st.CreateResource("t1", src); err == nil {
		t.Error("expected error on duplicate resource, got nil")
	}
}

// --- UsageLog ---

func TestUsageLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")

	entries, err := ReadUsageLog(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("expected empty log, got err=%v entries=%d", err, len(entries))
	}

	e1 := UsageEntry{Topic: "t1", Profile: "haiku", InputTokens: 100, OutputTokens: 50}
	e2 := UsageEntry{Topic: "t2", Profile: "sonnet", InputTokens: 200, OutputTokens: 80}
	if err := AppendUsageLog(path, e1); err != nil {
		t.Fatal(err)
	}
	if err := AppendUsageLog(path, e2); err != nil {
		t.Fatal(err)
	}

	loaded, err := ReadUsageLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded))
	}

	agg := AggregateUsage(loaded, "", 0)
	if agg.Total.Calls != 2 {
		t.Errorf("total calls = %d, want 2", agg.Total.Calls)
	}
	if agg.Total.InputTokens != 300 {
		t.Errorf("total input = %d, want 300", agg.Total.InputTokens)
	}
	if len(agg.ByProfile) != 2 {
		t.Errorf("expected 2 profiles, got %d", len(agg.ByProfile))
	}

	// Topic filter.
	filtered := AggregateUsage(loaded, "t1", 0)
	if filtered.Total.Calls != 1 || filtered.Total.InputTokens != 100 {
		t.Errorf("filtered agg unexpected: %+v", filtered.Total)
	}
}

// --- resolvePrompt helpers (via flag args simulation) ---

func TestResolveSystemText(t *testing.T) {
	// Inline text.
	text, ok := resolveSystemTextHelper("hello", "")
	if !ok || text != "hello" {
		t.Errorf("inline: got (%q, %v)", text, ok)
	}

	// File path.
	f, err := os.CreateTemp("", "sys*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("from file")
	f.Close()

	text, ok = resolveSystemTextHelper("", f.Name())
	if !ok || text != "from file" {
		t.Errorf("file: got (%q, %v)", text, ok)
	}

	// Missing file.
	_, ok = resolveSystemTextHelper("", "/nonexistent/path/sys.txt")
	if ok {
		t.Error("expected ok=false for missing file")
	}
}

// resolveSystemTextHelper is a copy of the logic in headless.go, tested here
// without depending on the main package (which has flag state).
func resolveSystemTextHelper(inline, filePath string) (string, bool) {
	if filePath != "" {
		b, err := os.ReadFile(filePath)
		if err != nil {
			return "", false
		}
		return string(b), true
	}
	return inline, true
}

func TestYesNo(t *testing.T) {
	// Verify the helper via strings — duplicated logic is intentional
	// (avoids importing main package from store_test).
	yesNo := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	if yesNo(true) != "yes" {
		t.Error("yesNo(true) != yes")
	}
	if yesNo(false) != "no" {
		t.Error("yesNo(false) != no")
	}
}

func TestTotalHistoryTokens(t *testing.T) {
	h := core.NewHistory()
	h.Append(core.RoleUser, strings.Repeat("a", 400))    // 100 tokens
	h.Append(core.RoleAssistant, strings.Repeat("b", 40)) // 10 tokens
	total := 0
	for _, m := range h.Msgs {
		total += core.ApproxTokens(m.Content)
	}
	if total != 110 {
		t.Errorf("expected 110 tokens, got %d", total)
	}
}
