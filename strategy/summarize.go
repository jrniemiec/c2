package strategy

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jrniemiec/c2/core"
)

// SummarizeStrategy keeps recent messages verbatim and compresses older history
// into a rolling summary using a secondary LLM call. The summary is cached in
// summary.txt and reused across calls.
type SummarizeStrategy struct {
	SummarizerProvider core.Provider
	SummarizerBudget   int // token budget available for summarizer input
	TopicName          string
	Store              core.Store
	Ctx                context.Context
	Out                io.Writer // nil = quiet
	Budget             int       // effective budget for main model
	VerbatimRatio      float64   // fraction of budget kept verbatim (default 0.4)
}

func (s *SummarizeStrategy) Name() string { return StrategySummarize }

func (s *SummarizeStrategy) Apply(h *core.History, prompt string) []core.Message {
	if h == nil || len(h.Msgs) == 0 {
		return nil
	}
	if s.Budget <= 0 {
		return nil
	}

	msgs := h.NonNoteMsgs()

	verbatimBudget := int(float64(s.Budget) * s.VerbatimRatio)

	summaryText, coversThrough, err := s.Store.LoadSummary(s.TopicName)
	if err != nil {
		s.warnf("failed to load summary: %v — falling back to token-budget", err)
		return s.tokenBudgetFallback(h, prompt)
	}

	verbatimStart := 0
	if coversThrough >= 0 && coversThrough < len(msgs) {
		verbatimStart = coversThrough + 1
	} else if coversThrough >= len(msgs) {
		verbatimStart = len(msgs)
	}
	verbatimMsgs := msgs[verbatimStart:]

	summaryTokens := core.ApproxTokens(summaryText)
	verbatimTokens := totalTokens(verbatimMsgs)

	needsCompaction := false
	if summaryText == "" {
		allTokens := totalTokens(msgs)
		needsCompaction = allTokens > s.Budget
	} else {
		overflow := summaryTokens + verbatimTokens - s.Budget
		if overflow > 0 {
			overflowMsgs := identifyOverflow(verbatimMsgs, verbatimBudget)
			overflowTokens := totalTokens(overflowMsgs)
			needsCompaction = overflowTokens > int(float64(verbatimBudget)*0.2)
		}
	}

	if needsCompaction {
		newSummary, newCoversThrough, ok := s.compactMsgs(msgs, summaryText, coversThrough, verbatimStart, verbatimBudget)
		if ok {
			summaryText = newSummary
			coversThrough = newCoversThrough
			verbatimStart = coversThrough + 1
			if verbatimStart > len(msgs) {
				verbatimStart = len(msgs)
			}
			verbatimMsgs = msgs[verbatimStart:]
		}
	}

	return s.buildContext(summaryText, verbatimMsgs, verbatimBudget, prompt)
}

// compactMsgs is like compact but operates on a pre-filtered message slice
// (notes already excluded) instead of h.Msgs directly.
func (s *SummarizeStrategy) compactMsgs(
	msgs []core.Message,
	existingSummary string,
	oldCoversThrough int,
	verbatimStart int,
	verbatimBudget int,
) (string, int, bool) {
	verbatimMsgs := msgs[verbatimStart:]
	overflowMsgs := identifyOverflow(verbatimMsgs, verbatimBudget)
	if len(overflowMsgs) == 0 && existingSummary == "" {
		return existingSummary, oldCoversThrough, false
	}

	if s.SummarizerBudget > 0 {
		inputLimit := int(float64(s.SummarizerBudget)*0.8) - core.ApproxTokens(existingSummary)
		if inputLimit < 0 {
			inputLimit = 0
		}
		overflowMsgs = trimToTokenLimit(overflowMsgs, inputLimit)
	}

	if s.Out != nil {
		allTokens := totalTokens(msgs)
		summaryTokens := core.ApproxTokens(existingSummary)
		verbatimTokens := totalTokens(verbatimMsgs)
		fmt.Fprintf(s.Out, "Compacting history for topic '%s'\n", s.TopicName)
		fmt.Fprintf(s.Out, "  history:         %d messages (~%s tokens)\n", len(msgs), core.FormatTokens(allTokens))
		if existingSummary != "" {
			fmt.Fprintf(s.Out, "  summary covers:  messages 1-%d (~%s tokens)\n", oldCoversThrough+1, core.FormatTokens(summaryTokens))
		}
		fmt.Fprintf(s.Out, "  verbatim window: %d messages (~%s tokens)\n", len(verbatimMsgs), core.FormatTokens(verbatimTokens))
		fmt.Fprintf(s.Out, "  compacting:      %d overflow messages\n", len(overflowMsgs))
	}

	var sb strings.Builder
	if existingSummary != "" {
		sb.WriteString("Previous summary:\n")
		sb.WriteString(existingSummary)
		sb.WriteString("\n\nNew exchanges to incorporate:\n")
	}
	for _, m := range overflowMsgs {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
	}

	summarizationPrompt := `You are a document summarizer. You will receive a long technical conversation covering multiple topics.

Your task: produce one unified summary covering ALL topics in the conversation from start to finish.

For each distinct topic found in the conversation:
- Write a topic heading
- Write 2-4 bullet points with specific facts, exact commands, variable names, or decisions

Rules:
- Cover every topic present, in the order they appear
- Preserve exact syntax: commands, flags, function names, variable names, code snippets
- Do not generalize — state the actual fact
- No introduction, no conclusion, no meta-commentary

Conversation to summarize:

` + sb.String()

	newSummaryText, _, err := s.SummarizerProvider.Chat(
		s.Ctx,
		"",
		[]core.Message{{Role: core.RoleUser, Content: summarizationPrompt}},
	)
	if err != nil {
		s.warnf("history compaction failed: %v — sending partial context", err)
		return existingSummary, oldCoversThrough, false
	}

	newCoversThrough := verbatimStart + len(overflowMsgs) - 1

	if s.Out != nil {
		remainingVerbatim := msgs[newCoversThrough+1:]
		remainingTokens := totalTokens(remainingVerbatim)
		totalCtx := core.ApproxTokens(newSummaryText) + remainingTokens
		fmt.Fprintf(s.Out, "  summary updated: covers messages 1-%d\n", newCoversThrough+1)
		fmt.Fprintf(s.Out, "  context window:  ~%s summary + ~%s verbatim = ~%s total\n",
			core.FormatTokens(core.ApproxTokens(newSummaryText)),
			core.FormatTokens(remainingTokens),
			core.FormatTokens(totalCtx))
	}

	if err := s.Store.SaveSummary(s.TopicName, newSummaryText, newCoversThrough); err != nil {
		s.warnf("failed to save summary: %v", err)
	}
	return newSummaryText, newCoversThrough, true
}

func (s *SummarizeStrategy) compact(
	h *core.History,
	existingSummary string,
	oldCoversThrough int,
	verbatimStart int,
	verbatimBudget int,
) (string, int, bool) {
	verbatimMsgs := h.Msgs[verbatimStart:]
	overflowMsgs := identifyOverflow(verbatimMsgs, verbatimBudget)
	if len(overflowMsgs) == 0 && existingSummary == "" {
		return existingSummary, oldCoversThrough, false
	}

	if s.SummarizerBudget > 0 {
		inputLimit := int(float64(s.SummarizerBudget)*0.8) - core.ApproxTokens(existingSummary)
		if inputLimit < 0 {
			inputLimit = 0
		}
		overflowMsgs = trimToTokenLimit(overflowMsgs, inputLimit)
	}

	if s.Out != nil {
		allTokens := totalTokens(h.Msgs)
		summaryTokens := core.ApproxTokens(existingSummary)
		verbatimTokens := totalTokens(verbatimMsgs)
		newCoversIdx := verbatimStart + len(overflowMsgs) - 1
		if newCoversIdx < 0 {
			newCoversIdx = len(h.Msgs) - 1
		}
		_ = newCoversIdx
		fmt.Fprintf(s.Out, "Compacting history for topic '%s'\n", s.TopicName)
		fmt.Fprintf(s.Out, "  history:         %d messages (~%s tokens)\n", len(h.Msgs), core.FormatTokens(allTokens))
		if existingSummary != "" {
			fmt.Fprintf(s.Out, "  summary covers:  messages 1-%d (~%s tokens)\n", oldCoversThrough+1, core.FormatTokens(summaryTokens))
		}
		fmt.Fprintf(s.Out, "  verbatim window: %d messages (~%s tokens)\n", len(verbatimMsgs), core.FormatTokens(verbatimTokens))
		fmt.Fprintf(s.Out, "  compacting:      %d overflow messages\n", len(overflowMsgs))
	}

	var sb strings.Builder
	if existingSummary != "" {
		sb.WriteString("Previous summary:\n")
		sb.WriteString(existingSummary)
		sb.WriteString("\n\nNew exchanges to incorporate:\n")
	}
	for _, m := range overflowMsgs {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
	}

	summarizationPrompt := `You are a document summarizer. You will receive a long technical conversation covering multiple topics.

Your task: produce one unified summary covering ALL topics in the conversation from start to finish.

For each distinct topic found in the conversation:
- Write a topic heading
- Write 2-4 bullet points with specific facts, exact commands, variable names, or decisions

Rules:
- Cover every topic present, in the order they appear
- Preserve exact syntax: commands, flags, function names, variable names, code snippets
- Do not generalize — state the actual fact
- No introduction, no conclusion, no meta-commentary

Conversation to summarize:

` + sb.String()

	newSummaryText, _, err := s.SummarizerProvider.Chat(
		s.Ctx,
		"",
		[]core.Message{{Role: core.RoleUser, Content: summarizationPrompt}},
	)
	if err != nil {
		s.warnf("history compaction failed: %v — sending partial context", err)
		return existingSummary, oldCoversThrough, false
	}

	newCoversThrough := verbatimStart + len(overflowMsgs) - 1

	if s.Out != nil {
		remainingVerbatim := h.Msgs[newCoversThrough+1:]
		remainingTokens := totalTokens(remainingVerbatim)
		totalCtx := core.ApproxTokens(newSummaryText) + remainingTokens
		fmt.Fprintf(s.Out, "  summary updated: covers messages 1-%d\n", newCoversThrough+1)
		fmt.Fprintf(s.Out, "  context window:  ~%s summary + ~%s verbatim = ~%s total\n",
			core.FormatTokens(core.ApproxTokens(newSummaryText)),
			core.FormatTokens(remainingTokens),
			core.FormatTokens(totalCtx))
	}

	if err := s.Store.SaveSummary(s.TopicName, newSummaryText, newCoversThrough); err != nil {
		s.warnf("failed to save summary: %v", err)
	}
	return newSummaryText, newCoversThrough, true
}

func (s *SummarizeStrategy) buildContext(summaryText string, verbatimMsgs []core.Message, verbatimBudget int, prompt string) []core.Message {
	remaining := verbatimBudget - core.ApproxTokens(prompt)
	var selected []core.Message
	used := 0
	for i := len(verbatimMsgs) - 1; i >= 0; i-- {
		cost := core.ApproxTokens(verbatimMsgs[i].Content)
		if used+cost > remaining {
			break
		}
		used += cost
		selected = append(selected, verbatimMsgs[i])
	}
	for l, r := 0, len(selected)-1; l < r; l, r = l+1, r-1 {
		selected[l], selected[r] = selected[r], selected[l]
	}

	var out []core.Message
	if summaryText != "" {
		out = append(out, core.Message{
			Role:    core.RoleAssistant,
			Content: "[Context summary]\n" + summaryText,
		})
	}
	return append(out, selected...)
}

func (s *SummarizeStrategy) tokenBudgetFallback(h *core.History, prompt string) []core.Message {
	fb := &TokenBudgetStrategy{Budget: s.Budget, ReserveTokens: 512}
	return fb.Apply(h, prompt)
}

func (s *SummarizeStrategy) warnf(format string, args ...any) {
	if s.Out == nil {
		return
	}
	fmt.Fprintf(s.Out, "Warning: "+format+"\n", args...)
}

func identifyOverflow(msgs []core.Message, verbatimBudget int) []core.Message {
	total := totalTokens(msgs)
	if total <= verbatimBudget {
		return nil
	}
	excess := total - verbatimBudget
	var overflow []core.Message
	accumulated := 0
	for _, m := range msgs {
		if accumulated >= excess {
			break
		}
		accumulated += core.ApproxTokens(m.Content)
		overflow = append(overflow, m)
	}
	return overflow
}

func trimToTokenLimit(msgs []core.Message, limit int) []core.Message {
	total := totalTokens(msgs)
	if total <= limit {
		return msgs
	}
	for len(msgs) > 0 && total > limit {
		total -= core.ApproxTokens(msgs[0].Content)
		msgs = msgs[1:]
	}
	return msgs
}

func totalTokens(msgs []core.Message) int {
	total := 0
	for _, m := range msgs {
		total += core.ApproxTokens(m.Content)
	}
	return total
}
