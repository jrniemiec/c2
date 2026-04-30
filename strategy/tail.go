package strategy

import "github.com/jrniemiec/c2/core"

// TailStrategy keeps the last N user turns of history.
type TailStrategy struct {
	MaxUserMessages int
}

func (s *TailStrategy) Name() string { return StrategyTail }

func (s *TailStrategy) Apply(h *core.History, _ string) []core.Message {
	return h.ToMessages(s.MaxUserMessages)
}
