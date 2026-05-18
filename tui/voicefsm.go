package tui

import "strings"

// VoiceState is the current state of the voice interaction FSM.
type VoiceState int

const (
	VoiceIdle       VoiceState = iota // KWS only; waiting for wake word
	VoiceAwake                        // wake word heard; listening for command
	VoiceDictating                    // recording prompt/note via VAD+STT
	VoiceExecuting                    // running an action (LLM call, TTS, meta-cmd)
	VoiceConversing                   // continuous conversation; VAD+STT per turn
	VoicePaused                       // text mode active; pipeline idles (no KWS/VAD/STT)
)

func (s VoiceState) String() string {
	switch s {
	case VoiceIdle:
		return "IDLE"
	case VoiceAwake:
		return "AWAKE"
	case VoiceDictating:
		return "DICTATING"
	case VoiceExecuting:
		return "EXECUTING"
	case VoiceConversing:
		return "CONVERSING"
	case VoicePaused:
		return "PAUSED"
	}
	return "UNKNOWN"
}

// activeKeywords returns the set of @label values that the TUI should act on
// when in this state. All keywords still run through the spotter; filtering
// is done here to avoid false-state transitions.
func (s VoiceState) activeKeywords() map[string]bool {
	switch s {
	case VoiceIdle:
		return map[string]bool{
			"computer": true,
		}
	case VoiceAwake:
		// Commands come from STT now; KWS only watches for "computer" interrupt.
		return map[string]bool{
			"computer": true,
		}
	case VoiceDictating:
		return map[string]bool{
			"computer": true,
		}
	case VoiceExecuting:
		return map[string]bool{
			"computer": true,
		}
	case VoiceConversing:
		return map[string]bool{
			"computer": true,
		}
	}
	return nil
}

// Bubbletea message types for voice FSM events.

// voiceKWSEventMsg is sent by the pipeline goroutine when a keyword fires.
type voiceKWSEventMsg struct{ keyword string }

// voiceAwakeTimeoutMsg fires when the AWAKE state timeout expires.
type voiceAwakeTimeoutMsg struct{}

// voiceExecutingTimeoutMsg fires when EXECUTING has no follow-up for 5s.
type voiceExecutingTimeoutMsg struct{}

// voiceLevelMsg carries a new RMS mic level sample from the pipeline goroutine.
type voiceLevelMsg struct{ level float32 }

// voiceStateChangePipelineMsg is an internal message (pipeline-side only)
// carrying the new FSM state so the pipeline can gate VAD+STT.
// Sent via stateChangeCh, not the Bubbletea loop.

// commandSynonyms maps each command label to the spoken phrases that trigger it.
// Matching is done after lowercasing and stripping common filler words.
var commandSynonyms = map[string][]string{
	"clear":           {"clear"},
	"stop":            {"stop", "cancel", "stop playback", "stop it"},
	"resume":          {"resume", "keep going", "resume playback"},
	"session_start":   {"start session", "session start", "start conversation", "conversation start", "start chat", "chat start"},
	"session_end":     {"stop session", "session stop", "stop conversation", "conversation stop", "stop chat", "chat stop", "leave session", "leave conversation", "leave chat"},
	"session_resume":  {"continue", "never mind", "thank you", "thanks", "thank you very much"},
	"clear_input":     {"scratch that", "clear that", "start over", "clear buffer"},
	"ask_llm":        {"ask", "question", "query", "search", "ask llm"},
	"chat_note":      {"note", "take note", "save note", "chat note"},
	"chat_replay":    {"replay", "replay that", "play again", "repeat", "chat replay"},
	"chat_play_last": {"play last", "play it", "last message"},
	"chat_play_all":  {"play all", "read all", "play everything", "chat play all"},
	"config":          {"config", "configuration", "show config", "show configuration"},
	"status":          {"status", "show status"},
	"voice_status":    {"your status", "what's your status", "what is your status"},
	"stats":           {"stats", "statistics", "show stats"},
	"voice_commands":  {"voice commands", "list voice commands", "show voice commands", "what can I say"},
	"fold":            {"fold", "fold all", "collapse", "collapse all"},
	"unfold":          {"unfold", "unfold all", "expand", "expand all"},
	"play_all":        {"play all", "read all", "play everything"},
	"tts_toggle":      {"toggle tts", "tts toggle", "tts on", "tts off", "toggle speech"},
	"topic_info":      {"topic info", "show topic", "current topic"},
	"topic_default":   {"topic default", "show default topic", "default topic info"},
	"profile_info":    {"profile info", "show profile", "current profile"},
	"profile_default": {"profile default", "show default profile", "default profile info"},
	"system_show":     {"system prompt", "show system", "show system prompt"},
	"system_clear":    {"clear system", "clear system prompt", "remove system prompt"},
	"help":            {"help", "show help", "commands"},
	"delete_last":     {"delete last", "remove last", "undo"},
	"topic_list":        {"list topics", "show topics", "topics", "topic list"},
	"topic_clear":       {"clear topic", "clear chat", "clear history", "topic clear"},
	"topic_delete":      {"delete topic", "remove topic", "topic delete"},
	"topic_new":         {"new topic", "create topic", "topic new"},
	"topic_switch":      {"switch topic", "change topic", "topic switch"},
	"topic_default_set": {"set default topic", "topic default set", "default topic"},
	"topic_summary":     {"topic summary", "summarize topic", "summary"},
	"topic_history":     {"topic history", "show history", "history"},
	"profile_list":      {"list profiles", "show profiles", "profiles", "profile list"},
	"profile_switch":    {"switch profile", "change profile", "change model", "profile switch"},
	"profile_default_set": {"set default profile", "profile default set", "default profile"},
	"resource_list":     {"list resources", "resources", "show resources", "resource list"},
	"resource_remove":   {"remove resource", "delete resource", "resource remove", "resource delete"},
	"resource_view":   {"view resource", "open resource", "show resource", "resource view"},
	"resource_edit":   {"edit resource", "resource edit"},
	"export":          {"export", "save conversation", "export conversation"},
	"correct_input":   {"correct that", "fix that", "grammar check", "fix grammar", "correct note", "fix note"},
	"copy_input":      {"copy input", "copy prompt"},
	"open_log":        {"show log", "open log", "debug log", "view log", "close log", "hide log"},
}

// fillerWords are stripped from the transcript before matching.
var fillerWords = []string{"please", "can you", "could you", "hey", "um", "uh", "like", "just"}

// matchVoiceCommand normalises text and tries to match it against commandSynonyms.
// Returns the command label, or "" if no match.
func matchVoiceCommand(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	// Strip trailing punctuation that STT models commonly append.
	t = strings.TrimRight(t, ".!?,;:")
	t = strings.TrimSpace(t)
	for _, filler := range fillerWords {
		t = strings.ReplaceAll(t, filler+" ", "")
		t = strings.TrimPrefix(t, filler)
	}
	t = strings.TrimSpace(t)

	// Exact match first.
	for label, phrases := range commandSynonyms {
		for _, p := range phrases {
			if t == p {
				return label
			}
		}
	}
	// Substring match (transcript may include trailing words).
	for label, phrases := range commandSynonyms {
		for _, p := range phrases {
			if strings.HasPrefix(t, p) || strings.Contains(t, p) {
				return label
			}
		}
	}
	return ""
}

// suspendedTTS holds TTS state saved on a "computer" interrupt.
type suspendedTTS struct {
	sentences  []ttsPendingItem
	streamBuf  string
	ttsQueue   []int // play-all exchange indices not yet started
}
