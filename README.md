# c2

A TUI-based command-and-control app for interacting with LLMs, bootstrapped from [lore](https://github.com/jrniemiec/lore). c2 adds a **voice layer** — you can speak to it, and it speaks back. Meta-commands ("replay that", "switch topic", "stop") are recognised locally without hitting the LLM.

---

## Features

- **Voice + text input** — speak or type, switch modes with Tab
- **Full voice pipeline** — keyword wake word (KWS) → VAD → STT → command recognition or LLM
- **TTS readout** — Kokoro (neural) or macOS `say` backend
- **Resource viewer** — full-screen in-app overlay with TTS playback from cursor position
- **Topics, profiles, system prompts, resources, stats**
- **Self-contained** — all data and config stored in `~/.c2/`

---

## Installation

### Prerequisites

- Go 1.22+
- macOS (audio via PortAudio; sherpa-onnx ships macOS-only binaries)
- `portaudio` headers (`brew install portaudio`)

### Build

```bash
git clone https://github.com/jrniemiec/c2
cd c2
make build          # builds to ./bin/c2
make install        # builds + installs to ~/dev/bin/c2 (with codesign)
```

Never use bare `go build` — always go through `make`.

---

## Configuration

All configuration and data live under `~/.c2/`.

### Config file location

```
~/.c2/config.json       main config (LLM profiles + voice settings)
~/.c2/debug.log         voice pipeline debug log
```

### c2 config section

The config file contains LLM profile settings and a `"c2"` section for voice/audio. Example:

```json
{
  "profile": "...",
  "c2": {
    "tts_backend":   "kokoro",
    "tts_model":     "/path/to/kokoro/model.onnx",
    "tts_voices":    "/path/to/kokoro/voices.bin",
    "tts_tokens":    "/path/to/kokoro/tokens.txt",
    "tts_data_dir":  "/path/to/espeak-ng-data",
    "tts_lexicon":   "",
    "tts_voice":     "en-us",
    "tts_speaker_id": 0,
    "tts_speed":     1.0,

    "vad_model":     "/path/to/silero_vad.onnx",

    "stt_encoder":   "/path/to/encoder.int8.onnx",
    "stt_decoder":   "/path/to/decoder.int8.onnx",
    "stt_tokens":    "/path/to/tokens.txt",
    "stt_language":  "en",

    "kws_encoder":   "/path/to/kws-encoder.onnx",
    "kws_decoder":   "/path/to/kws-decoder.onnx",
    "kws_joiner":    "/path/to/kws-joiner.onnx",
    "kws_tokens":    "/path/to/kws-tokens.txt",
    "kws_keywords":  "/path/to/c2_keywords.txt",
    "kws_gain":      1.0,

    "input_device":  ""
  }
}
```

| Field | Description |
|---|---|
| `tts_backend` | `"kokoro"` (neural) or `"say"` (macOS built-in) |
| `tts_voice` | Language tag for Kokoro (`en-us`) or voice name for `say` (`Samantha`) |
| `tts_speaker_id` | Speaker index for Kokoro (default 0) |
| `tts_speed` | Words/min for `say` (default 200) or speed multiplier for Kokoro (default 1.0) |
| `vad_model` | Path to Silero VAD `.onnx` model |
| `stt_encoder/decoder/tokens` | sherpa-onnx transducer STT model files |
| `stt_language` | STT language code (default `"en"`) |
| `kws_*` | sherpa-onnx keyword spotter model files |
| `kws_keywords` | File listing wake words, one per line prefixed with `@label` |
| `kws_gain` | Amplitude boost applied to KWS input only (0 = disabled) |
| `input_device` | Microphone device name (empty = system default) |

Voice is enabled automatically when `vad_model`, `stt_encoder`, and `stt_decoder` are set. KWS (wake word) is optional; without it voice is activated manually with Tab.

---

## Startup

```
c2 [flags]
```

### Core flags

| Flag | Short | Description |
|---|---|---|
| `--topic <name>` | `-t` | Open specific topic |
| `--profile <name>` | `-p` | Use specific profile |
| `--model <name>` | `-m` | Override model |
| `--text-mode` | | Disable voice; keyboard only |
| `--theme <auto\|light\|dark>` | | Color theme (default: auto) |
| `--fold-lines <N>` | | Fold entries longer than N lines (default: 20, 0=never) |
| `--fold-on-start` | | Start with all long entries folded |
| `--chat-labels` | | Prefix turns with [you]/[profile] (default: true) |
| `--no-tui` / `-nw` | | Headless/CLI mode |
| `--tts` | | Speak response in CLI mode |
| `--version` | `-v` | Print version and exit |

### Admin flags (headless)

| Flag | Description |
|---|---|
| `--topic-list` | List all topics |
| `--topic-new <name>` | Create topic |
| `--topic-delete` | Delete current topic |
| `--topic-clear` | Erase topic history |
| `--topic-default-set <name>` | Set default topic |
| `--profile-list` | List profiles |
| `--profile-default-set <name>` | Set default profile |
| `--resource-list` | List topic resources |
| `--resource-remove <name>` | Delete resource |
| `--topic-resource <path>` | Copy file to topic resources |
| `--system-set <text>` | Set system prompt |
| `--system-file <path>` | Set system prompt from file |
| `--stats` | Show usage and cost |
| `--status` | Show effective defaults |
| `--config` | Print resolved config |
| `--delete-last [N]` | Delete last N exchanges |

---

## TUI Layout

```
┌──────────────────────────────────────────────────────────────┐
│ c2  topic: default  profile: gpt4  ● VOICE MODE             │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  CONVERSATION PANE (scrollable)                              │
│  [you] hello                                                 │
│  [gpt4] Hello! How can I help?                               │
│                                                              │
├──────────────────────────────────────────────────────────────┤
│  > type a message or speak after wake word                   │
├──────────────────────────────────────────────────────────────┤
│  42 in · 18 out · 340ms · $0.001   topic: 3 · $0.01         │
└──────────────────────────────────────────────────────────────┘
```

The **status bar** is context-sensitive:
- **Idle:** token counts, latency, cost
- **Slash command active:** command completions list
- **Command output:** scrollable results pane
- **Streaming:** `❄ streaming ●●●`
- **Transcribing:** `❄ transcribing ●●●`
- **TTS playing:** `♪ #3  1.2x  [ slower ] faster`

---

## Key Bindings

### Global

| Key | Action |
|---|---|
| `Enter` | Send prompt |
| `Ctrl+J` | Insert newline (multi-line input) |
| `Tab` | Fill completion (input) / toggle voice/text mode |
| `Esc` | Close pane / clear input / return to input |
| `Ctrl+C` | Cancel streaming / quit |
| `Ctrl+L` | Clear screen |
| `Ctrl+T` | Switch topic (completion list) |
| `Ctrl+P` | Switch profile (completion list) |
| `Ctrl+N` | Toggle focus: input ↔ conversation |
| `Ctrl+S` | Copy current entry to clipboard |

### Conversation pane (when focused)

| Key | Action |
|---|---|
| `↑` / `↓` | Navigate exchanges |
| `PgUp` / `PgDn` | Scroll content |
| `f` | Fold/unfold current entry |
| `s` | Speak current entry (TTS) |

### TTS playback

| Key | Action |
|---|---|
| `[` | Decrease speed |
| `]` | Increase speed |
| `Space` / `s` | Stop playback |

### Resource viewer overlay

| Key | Action |
|---|---|
| `↑` / `↓` | Move cursor line by line |
| `PgUp` / `PgDn` | Move cursor by half page |
| `g` | Jump to top |
| `G` | Jump to bottom |
| `s` | Start TTS from cursor position (or stop if playing) |
| `e` | Open in `$EDITOR` |
| `[` / `]` | Decrease / increase TTS speed |
| `Ctrl+X` / `q` | Close overlay |

---

## Slash Commands

Type `/` in the input pane to bring up completions.

### Topic

| Command | Description |
|---|---|
| `/topic [name]` | Show current topic info |
| `/topic-switch <name>` | Switch to topic |
| `/topic-new <name>` | Create and switch to topic |
| `/topic-list` | List all topics |
| `/topic-delete [name]` | Delete topic |
| `/topic-clear` | Erase topic history |
| `/topic-default` | Show default topic |
| `/topic-default-set <name>` | Set default topic |
| `/topic-summary` | Show context summary |
| `/topic-history [n]` | Show last N exchanges |

### Resource

| Command | Description |
|---|---|
| `/resource-add <file>` | Copy file to topic resources |
| `/resource-list [topic]` | List topic resources |
| `/resource-view <name>` | Open resource in full-screen overlay |
| `/resource-edit <name>` | Edit resource in `$EDITOR` |
| `/resource-remove <name>` | Delete resource |
| `/resource-delete <name>` | Delete resource (alias) |

Resource files are referenced in prompts with `@name`. Supported path forms:

```
@resource-name         topic resources folder
@subdir/name           nested resource
@./relative/path       relative to cwd
@/absolute/path        absolute path
@~/path                home-relative path
```

### Profile

| Command | Description |
|---|---|
| `/profile [name]` | Show profile info |
| `/profile-switch <name>` | Switch profile |
| `/profile-list` | List all profiles |
| `/profile-default` | Show default profile |
| `/profile-default-set <name>` | Set default profile |

### System prompt

| Command | Description |
|---|---|
| `/system` | Show system prompt |
| `/system-set <text>` | Set system prompt |
| `/system-clear` | Remove system prompt |

### Info

| Command | Description |
|---|---|
| `/config` | Show resolved config |
| `/status` | Show effective defaults |
| `/stats` | Usage and cost stats |
| `/voice-commands` | List all voice phrases |
| `/help [group]` | Show help (groups: topic resource profile system session info notes files nav theme view) |
| `/delete-last [n]` | Delete last N exchanges |

### View

| Command | Description |
|---|---|
| `/tts [on\|off]` | Toggle auto-speech (no arg = toggle) |
| `/play-all` | Speak all entries in conversation |
| `/fold` | Collapse long entries |
| `/unfold` | Expand long entries |
| `/theme [light\|dark\|auto]` | Switch or show theme |

---

## Voice Mode

### Wake word

Say **"Computer"** to wake c2. The pipeline is:

```
Microphone
  └─► KWS (always on) ─► "computer" detected
        └─► AWAKE state: energy buffer + STT
              ├─► command match ─► execute locally
              └─► no match ─► send to LLM
```

### Voice FSM states

| State | Description |
|---|---|
| `IDLE` | KWS only; waiting for wake word |
| `AWAKE` | Wake word heard; listening for command (750ms silence or 4s cap) |
| `DICTATING` | Recording prompt via VAD+STT |
| `CONVERSING` | Continuous conversation; VAD+STT per turn |
| `EXECUTING` | LLM call or TTS in progress |
| `PAUSED` | Text mode active; pipeline idle |

### Voice commands

Speak naturally after the wake word. Filler words ("please", "um", "hey", "just") are stripped automatically.

#### Session

| Say | Action |
|---|---|
| "clear" | Clear input |
| "stop" / "cancel" | Stop TTS or cancel |
| "resume" / "keep going" | Resume TTS |
| "start session" | Enter conversation mode |
| "stop session" | Leave conversation mode |
| "continue" / "never mind" / "thank you" | Resume from EXECUTING |

#### Conversation

| Say | Action |
|---|---|
| "ask" / "question" / "query" | Send input to LLM |
| "note" / "take note" | Save note to history |
| "replay" / "repeat" | Re-speak last response |
| "play last" / "play it" | Speak last message |
| "play all" / "read all" | Speak all entries |
| "scratch that" / "clear that" | Clear input buffer |
| "delete last" / "undo" | Delete last exchange |
| "fold" / "collapse" | Fold long entries |
| "unfold" / "expand" | Expand entries |
| "toggle tts" / "tts on" / "tts off" | Toggle auto-speech |

#### Topic (accelerators — pre-fill + completion picker)

| Say | Action |
|---|---|
| "topic info" | Show topic info |
| "list topics" / "topics" | List all topics |
| "switch topic" / "change topic" | Pre-fill `/topic-switch` + picker |
| "new topic" | Pre-fill `/topic-new` |
| "clear topic" / "clear history" | Clear topic history |
| "delete topic" | Pre-fill `/topic-delete` + picker |
| "topic default" | Show default topic |
| "set default topic" | Pre-fill `/topic-default-set` + picker |
| "topic summary" | Show summary |
| "topic history" | Show history |

#### Profile (accelerators)

| Say | Action |
|---|---|
| "profile info" | Show profile info |
| "list profiles" / "profiles" | List all profiles |
| "switch profile" / "change model" | Pre-fill `/profile-switch` + picker |
| "profile default" | Show default profile |
| "set default profile" | Pre-fill `/profile-default-set` + picker |

#### Resource (accelerators)

| Say | Action |
|---|---|
| "list resources" / "resources" | List topic resources |
| "remove resource" / "delete resource" | Pre-fill `/resource-remove` + picker |
| "view resource" / "open resource" | Pre-fill `/resource-view` + picker |
| "edit resource" | Pre-fill `/resource-edit` + picker |

#### System

| Say | Action |
|---|---|
| "system prompt" / "show system" | Show system prompt |
| "clear system" | Remove system prompt |

#### Info

| Say | Action |
|---|---|
| "config" / "show config" | Show resolved config |
| "status" / "show status" | Show effective defaults |
| "stats" / "statistics" | Usage and cost |
| "voice commands" / "what can I say" | List voice phrases |
| "help" / "show help" | Show help |

### Voice accelerators

For commands that require a parameter (topic name, profile name, resource name), saying the command pre-fills the input and opens a completion picker. Use `↑`/`↓` to select, `Enter` to confirm — no need to spell out the name.

---

## Architecture

```
c2/
├── main.go            entry point, flag parsing
├── headless.go        CLI mode operations
├── audio/             PortAudio capture + playback
├── speech/            VAD (Silero), STT (sherpa-onnx), TTS (Kokoro), KWS
├── c2config/          voice/audio config
├── tui/               Bubbletea TUI
│   ├── tui.go         startup + voice pipeline goroutine
│   ├── model.go       state
│   ├── update.go      event handling + voice FSM
│   ├── view.go        rendering
│   ├── commands.go    slash command handlers
│   ├── keys.go        key bindings
│   └── voicefsm.go    FSM state types + command synonyms
├── config/            provider config
├── engine/            LLM engine
├── provider/          API providers
├── store/             topic persistence
└── strategy/          context strategy
```

**Key dependencies:**
- [charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [k2-fsa/sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) — VAD, STT, TTS, KWS via ONNX
- [gordonklaus/portaudio](https://github.com/gordonklaus/portaudio) — audio I/O

All topics, history, profiles, and config live in `~/.c2/`.
