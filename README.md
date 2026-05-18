# c2

A TUI-based command-and-control app for interacting with LLMs, bootstrapped from [lore](https://github.com/jrniemiec/lore). c2 adds a **voice layer** — you can speak to it, and it speaks back. Meta-commands ("replay that", "switch topic", "stop") are recognised locally without hitting the LLM.

---

## Features

- **Voice + text input** — speak or type, switch modes with Tab
- **Full voice pipeline** — keyword wake word (KWS) → VAD → STT → command recognition or LLM
- **Dual TTS backends** — Kokoro (neural) for reading exchange text; macOS `say` for short command responses ("Deleted", "Conversing", etc.); each independently configurable
- **Shell commands** — prefix input with `!` to run shell commands; output shown in the results pane
- **Resource viewer** — full-screen in-app overlay with TTS playback from cursor position
- **Note dictation** — voice "note" mode prefixes input with `//`; clipboard copy strips it
- **Input correction** — Ctrl+G or voice "correct that" sends input to an LLM for spell/grammar fix
- **System prompt editor** — `/system-set` with no args opens `$EDITOR` on the topic's `system.txt`
- **Topics, profiles, system prompts, resources, stats**
- **Structured logging** — `~/.c2/c2.log` with rotation; level controlled via `--log-level` or `C2_LOG_LEVEL`
- **Self-contained** — all data and config stored in `~/.c2/` (working directory on launch)

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

## First run

On first launch c2 creates `~/.c2/` with a default config and stays alive:

```
c2: first run — created /Users/you/.c2
c2: copied default config to /Users/you/.c2/config.json
c2:
c2: API keys are read from environment variables:
c2:   Anthropic  — ANTHROPIC_API_KEY  (or C2_ANTHROPIC_API_KEY)
c2:   OpenAI     — OPENAI_API_KEY     (or C2_OPENAI_API_KEY)
c2:   Ollama     — no key needed, set C2_OLLAMA_HOST if not localhost
c2:
c2: → edit /Users/you/.c2/config.json
c2:   make sure you set the default LLM profile and default topic to the values you want
c2: press Enter when ready to continue (or q to quit):
```

Edit the config, press Enter. c2 reloads and validates (`default_profile` set and exists). If there's an error it tells you what to fix and re-prompts.

### Voice model setup

After config is valid, c2 asks whether to download voice models (~500 MB):

```
c2: voice mode requires model downloads (~500 MB)
c2: download now? [Y/n]:
```

Downloading shows a live progress bar:

```
    [=========>          ] 45%  23.1 / 51.2 MB
```

When all models are downloaded:

```
c2: bootstrap complete — all models downloaded and config written
c2: continue? [Y/n]:
```

Press Enter or Y to launch the TUI directly — no need to restart c2.

---

## Configuration

All configuration and data live under `~/.c2/`.

```
~/.c2/config.json       main config (LLM profiles + voice settings)
~/.c2/c2.log            application log (use --log-level to control verbosity)
~/.c2/models/           downloaded voice models
```

### c2 config section

The config file contains LLM profile settings and a `"c2"` section for voice/audio:

```json
{
  "default_profile": "oai-mini",
  "default_topic": "default",
  "c2": {
    "tts_readout_backend":    "kokoro",
    "tts_readout_speed":      280,
    "tts_readout_voice":      "",
    "tts_readout_speaker_id": 0,

    "tts_command_speed":      200,
    "tts_command_voice":      "",

    "tts_model":    "~/.c2/models/kokoro-en-v0_19/model.onnx",
    "tts_voices":   "~/.c2/models/kokoro-en-v0_19/voices.bin",
    "tts_tokens":   "~/.c2/models/kokoro-en-v0_19/tokens.txt",
    "tts_data_dir": "~/.c2/models/kokoro-en-v0_19/espeak-ng-data",

    "vad_model":    "~/.c2/models/silero_vad.onnx",

    "stt_encoder":  "~/.c2/models/sherpa-onnx-whisper-tiny.en/tiny.en-encoder.int8.onnx",
    "stt_decoder":  "~/.c2/models/sherpa-onnx-whisper-tiny.en/tiny.en-decoder.int8.onnx",
    "stt_tokens":   "~/.c2/models/sherpa-onnx-whisper-tiny.en/tiny.en-tokens.txt",
    "stt_language": "en",

    "kws_encoder":  "~/.c2/models/sherpa-onnx-kws-zipformer-.../encoder.int8.onnx",
    "kws_decoder":  "~/.c2/models/sherpa-onnx-kws-zipformer-.../decoder.onnx",
    "kws_joiner":   "~/.c2/models/sherpa-onnx-kws-zipformer-.../joiner.int8.onnx",
    "kws_tokens":   "~/.c2/models/sherpa-onnx-kws-zipformer-.../tokens.txt",
    "kws_keywords": "~/.c2/models/sherpa-onnx-kws-zipformer-.../c2_keywords.txt",
    "kws_gain":     1.5,

    "correction_profile": "oai-mini"
  }
}
```

#### Readout TTS — exchange text (s key, replay, play-all)

| Field | Description |
|---|---|
| `tts_readout_backend` | `"kokoro"` (neural, default) or `"say"` (macOS built-in) |
| `tts_readout_speed` | Words/min — mapped to Kokoro speed as `wpm/200` (280 → 1.4×) |
| `tts_readout_voice` | Kokoro language tag (e.g. `"en-us"`); or `say` voice name |
| `tts_readout_speaker_id` | Kokoro speaker index (default 0) |
| `tts_model` / `tts_voices` / `tts_tokens` / `tts_data_dir` | Kokoro model file paths |

#### Command TTS — short responses ("Deleted", "Conversing", etc.)

Always uses macOS `say`. Interrupts and kills any running readout.

| Field | Description |
|---|---|
| `tts_command_speed` | Words/min for `say` (default 200) |
| `tts_command_voice` | `say` voice name (empty = system default) |

#### Other voice fields

| Field | Description |
|---|---|
| `vad_model` | Path to Silero VAD `.onnx` model |
| `stt_encoder/decoder/tokens` | sherpa-onnx Whisper STT model files |
| `stt_language` | STT language code (default `"en"`) |
| `kws_*` | sherpa-onnx keyword spotter model files |
| `kws_keywords` | File listing wake words (one per line, `@label` prefix) |
| `kws_gain` | Amplitude boost applied to KWS input (0 = disabled) |
| `correction_profile` | Profile used for Ctrl+G / "correct that" input correction (default: `"oai-mini"`) |

Voice is enabled automatically when `vad_model`, `stt_encoder`, and `stt_decoder` are set. KWS (wake word "Computer") is optional; without it, voice is activated manually with Tab.

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
| `--ack-all-deletions` | | Require confirmation for all deletions |
| `--chat-labels` | | Prefix turns with [you]/[profile] (default: true) |
| `--speak-corrected-note` | | Speak corrected text after Ctrl+G or "correct that" (default: true) |
| `--log-level <level>` | | Log level: `debug\|info\|warn\|error` (default: `info`, env: `C2_LOG_LEVEL`) |
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
│ c2  topic: default  profile: oai-mini  ● VOICE MODE          │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  CONVERSATION PANE (scrollable)                              │
│  [you] hello                                                 │
│  [oai-mini] Hello! How can I help?                           │
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
- **Correcting:** `❄ correcting ●●●`, then `✓ corrected` / `✓ no changes` for 2s
- **TTS playing:** `❄ ♪ #3  280 wpm  [ slower ] faster` (readout speed; `[`/`]` adjust it live)

---

## Key Bindings

### Global

| Key | Action |
|---|---|
| `Enter` | Send prompt / execute `!` shell command |
| `Ctrl+J` | Insert newline (multi-line input) |
| `Ctrl+G` | Send input for spell/grammar correction (replaced in-place) |
| `Tab` | Fill completion (input) / toggle voice/text mode |
| `Esc` | Close pane / clear input / kill TTS / return to input |
| `Ctrl+C` | Cancel streaming / quit |
| `Ctrl+L` | Clear screen |
| `Ctrl+T` | Switch topic (picker overlay) |
| `Ctrl+P` | Switch profile (picker overlay) |
| `Ctrl+N` | Toggle focus: input ↔ conversation |
| `Ctrl+S` | Copy focused exchange or input to clipboard |
| `Ctrl+X` | Close any overlay |

### Conversation pane (when focused)

| Key | Action |
|---|---|
| `↑` / `↓` | Navigate exchanges |
| `PgUp` / `PgDn` | Scroll content |
| `v` | Fold/unfold current entry |
| `s` | Speak current entry (TTS) |
| `x` | Delete current entry (with confirmation) |

### TTS playback (readout)

| Key | Action |
|---|---|
| `[` | Decrease readout speed (–20 wpm; Kokoro: takes effect next sentence) |
| `]` | Increase readout speed (+20 wpm; Kokoro: takes effect next sentence) |
| `s` | Stop playback |

### Resource viewer overlay

| Key | Action |
|---|---|
| `↑` / `↓` | Move cursor line by line |
| `PgUp` / `PgDn` | Move cursor by half page |
| `g` | Jump to top |
| `G` | Jump to bottom |
| `s` | Start TTS from cursor (line-by-line, cursor follows); stop if playing |
| `e` | Open in `$EDITOR` |
| `[` / `]` | Decrease / increase TTS speed (restarts current line) |
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
| `/resource-new <name>` | Create new resource file and open in `$EDITOR` |
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
| `/system-set [text]` | Set system prompt inline, or open `$EDITOR` when called with no args |
| `/system-clear` | Remove system prompt |

### Info & view

| Command | Description |
|---|---|
| `/config` | Show resolved config |
| `/status` | Show effective defaults |
| `/stats` | Usage and cost stats |
| `/voice-commands` | List all voice phrases |
| `/log` | Toggle log tail (`~/.c2/c2.log`) in a new terminal window |
| `/help [group]` | Show help (groups: topic resource profile system session info notes files nav theme view) |
| `/delete-last [n]` | Delete last N exchanges |
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

If the audio device disconnects (e.g. Bluetooth), the pipeline recovers automatically and resumes listening.

### Voice FSM states

| State | Description |
|---|---|
| `IDLE` | KWS only; waiting for wake word |
| `AWAKE` | Wake word heard; listening for command (750ms silence or 4s cap) |
| `DICTATING` | Recording prompt/note via VAD+STT |
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
| "continue" / "never mind" / "thank you" | Resume suspended TTS (if any) and return to prior state |

#### Conversation

| Say | Action |
|---|---|
| "ask" / "question" / "query" | Send input to LLM |
| "note" / "take note" | Save note to history (input prefixed with `//`) |
| "replay" / "repeat" | Re-speak last response |
| "play last" / "play it" | Speak last message |
| "play all" / "read all" | Speak all entries |
| "scratch that" / "clear that" | Clear input buffer |
| "delete last" / "undo" | Delete last exchange |
| "correct that" / "fix that" / "fix grammar" | Send input for spell/grammar correction |
| "copy input" / "copy prompt" | Copy input to clipboard (strips `//` in note mode) |
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
| "show log" / "open log" / "close log" | Toggle log tail window |
| "help" / "show help" | Show help |

### Voice accelerators

For commands that require a parameter (topic name, profile name, resource name), saying the command pre-fills the input and opens a completion picker. Use `↑`/`↓` to select, `Enter` to confirm — no need to spell out the name.

---

## Architecture

```
c2/
├── main.go            entry point, flag parsing, bootstrap flow
├── headless.go        CLI mode operations
├── audio/             PortAudio capture + playback
├── speech/            VAD (Silero), STT (sherpa-onnx), TTS (Kokoro), KWS
├── c2config/          voice/audio config
├── setup/             first-run bootstrap, model download with progress
├── tui/               Bubbletea TUI
│   ├── tui.go         startup + voice pipeline goroutine
│   ├── model.go       state
│   ├── update.go      event handling + voice FSM
│   ├── view.go        rendering
│   ├── commands.go    slash command handlers
│   ├── keys.go        key bindings
│   └── voicefsm.go    FSM state types + command synonyms
├── internal/clog/     structured logger (slog + lumberjack rotation)
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
