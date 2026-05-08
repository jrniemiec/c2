package core

import "testing"

// --- ApproxTokens ---

func TestApproxTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abcd", 1},
		{"abcde", 2},
		{"hello world", 3},
	}
	for _, c := range cases {
		if got := ApproxTokens(c.in); got != c.want {
			t.Errorf("ApproxTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// --- FormatTokens ---

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{1_000_000, "1.0M"},
		{2_500_000, "2.5M"},
	}
	for _, c := range cases {
		if got := FormatTokens(c.in); got != c.want {
			t.Errorf("FormatTokens(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- History.ToMessages ---

func TestHistoryToMessages(t *testing.T) {
	h := NewHistory()
	// Empty history.
	if msgs := h.ToMessages(10); len(msgs) != 0 {
		t.Fatalf("expected empty, got %d messages", len(msgs))
	}

	h.Append(RoleUser, "u1")
	h.Append(RoleAssistant, "a1")
	h.Append(RoleUser, "u2")
	h.Append(RoleAssistant, "a2")
	h.Append(RoleUser, "u3")
	h.Append(RoleAssistant, "a3")

	// maxUserMessages=2 should return last 2 user turns + their responses.
	msgs := h.ToMessages(2)
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "u2" {
		t.Errorf("expected first message to be u2, got %q", msgs[0].Content)
	}

	// maxUserMessages larger than total should return all.
	all := h.ToMessages(100)
	if len(all) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(all))
	}
}
