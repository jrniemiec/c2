package core

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	ttsCodeBlock  = regexp.MustCompile("(?s)```.*?```")
	ttsInlineCode = regexp.MustCompile("`[^`]+`")
	ttsURL        = regexp.MustCompile(`https?://\S+`)
	ttsBullets    = regexp.MustCompile(`(?m)^[\s]*[•●○◦▪▫–—\-\*]+\s*`)
	ttsMDSymbols  = regexp.MustCompile(`[*_#~|>\[\]{}\\]+`)
	ttsBoxDrawing = regexp.MustCompile(`[\x{2500}-\x{257F}\x{2580}-\x{259F}]+`) // box-drawing + block elements
	ttsDashRun    = regexp.MustCompile(`[-=~_+]{3,}`)                            // ---- ==== ~~~~
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
	s = ttsBoxDrawing.ReplaceAllString(s, "")
	s = ttsDashRun.ReplaceAllString(s, "")
	s = ttsMultiSpace.ReplaceAllString(s, " ")
	s = ttsMultiNL.ReplaceAllString(s, "\n\n")

	// Filter lines that are mostly non-alphanumeric (diagrams, table separators, etc).
	// A line where fewer than 30% of characters are letters or digits is skipped.
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			out = append(out, "")
			continue
		}
		total := 0
		alnum := 0
		for _, r := range trimmed {
			total++
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				alnum++
			}
		}
		if alnum*100/total >= 30 {
			out = append(out, line)
		}
	}

	return strings.TrimSpace(strings.Join(out, "\n"))
}
