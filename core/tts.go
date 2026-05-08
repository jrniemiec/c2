package core

import (
	"regexp"
	"strings"
)

var (
	ttsCodeBlock  = regexp.MustCompile("(?s)```.*?```")
	ttsInlineCode = regexp.MustCompile("`[^`]+`")
	ttsURL        = regexp.MustCompile(`https?://\S+`)
	ttsBullets    = regexp.MustCompile(`(?m)^[\s]*[•●○◦▪▫–—\-\*]+\s*`)
	ttsMDSymbols  = regexp.MustCompile(`[*_#~|>\[\]{}\\]+`)
	ttsMultiSpace = regexp.MustCompile(`[ \t]{2,}`)
	ttsMultiNL    = regexp.MustCompile(`\n{3,}`)
)

// TTSStrip removes markdown and other characters that cause say(1) to
// mispronounce or produce unwanted noise.
func TTSStrip(s string) string {
	s = ttsCodeBlock.ReplaceAllString(s, ". ")
	s = ttsInlineCode.ReplaceAllString(s, "")
	s = ttsURL.ReplaceAllString(s, "")
	s = ttsBullets.ReplaceAllString(s, "")
	s = ttsMDSymbols.ReplaceAllString(s, "")
	s = ttsMultiSpace.ReplaceAllString(s, " ")
	s = ttsMultiNL.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
