package strategy

import (
	"strings"

	"github.com/jrniemiec/c2/config"
	"github.com/jrniemiec/c2/core"
)

const (
	StrategyTail        = "tail"
	StrategyTokenBudget = "token-budget"
	StrategySummarize   = "summarize"
)

// ContextStrategy selects which messages from History to include in the context window.
type ContextStrategy interface {
	Name() string
	Apply(h *core.History, prompt string) []core.Message
}

// EffectiveBudget returns the token budget for a profile.
// Uses ContextTokenLimit if set, otherwise 60% of MaxContextTokens.
func EffectiveBudget(profile config.ProviderProfile) int {
	if profile.ContextTokenLimit > 0 {
		return profile.ContextTokenLimit
	}
	if profile.MaxContextTokens > 0 {
		return int(float64(profile.MaxContextTokens) * 0.6)
	}
	return 0
}

// EffectiveWindowMessages returns the tail strategy message limit.
func EffectiveWindowMessages(profile config.ProviderProfile, cfg config.Config) int {
	if profile.MaxUserMessages > 0 {
		return profile.MaxUserMessages
	}
	return cfg.WindowMessages
}

// EffectiveVerbatimRatio returns the verbatim ratio, defaulting to 0.4.
func EffectiveVerbatimRatio(profile config.ProviderProfile) float64 {
	if profile.VerbatimRatio > 0 {
		return profile.VerbatimRatio
	}
	return 0.4
}

// ResolveStrategyName returns the effective strategy name.
// Priority: explicit flag > profile.Strategy > auto-detect.
func ResolveStrategyName(flagStrategy string, profile config.ProviderProfile) string {
	if s := strings.TrimSpace(flagStrategy); s != "" {
		return s
	}
	if s := strings.TrimSpace(profile.Strategy); s != "" {
		return s
	}
	if strings.TrimSpace(profile.SummarizerProfile) != "" {
		return StrategySummarize
	}
	if EffectiveBudget(profile) > 0 {
		return StrategyTokenBudget
	}
	return StrategyTail
}

// New instantiates the strategy for the given name.
// overrideBudget > 0 takes precedence over profile token limits.
func New(name string, profile config.ProviderProfile, cfg config.Config, systemPrompt string, overrideBudget int) ContextStrategy {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case StrategyTokenBudget:
		budget := overrideBudget
		if budget <= 0 {
			budget = EffectiveBudget(profile)
		}
		if budget <= 0 {
			return &TailStrategy{MaxUserMessages: EffectiveWindowMessages(profile, cfg)}
		}
		reserve := core.ApproxTokens(systemPrompt) + 512
		return &TokenBudgetStrategy{Budget: budget, ReserveTokens: reserve}
	default:
		return &TailStrategy{MaxUserMessages: EffectiveWindowMessages(profile, cfg)}
	}
}
