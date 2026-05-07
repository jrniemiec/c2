package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattn/go-isatty"

	"github.com/jrniemiec/c2/c2config"
	"github.com/jrniemiec/c2/tui"
	"github.com/jrniemiec/lore/config"
	"github.com/jrniemiec/lore/engine"
)

// version is set at build time via -ldflags "-X main.version=x.y.z".
var version = "dev"

// --- flag variables -------------------------------------------------------

var (
	// mode
	flagNoTUI       bool
	flagTextMode    bool
	flagTheme       string
	flagChatLabels  bool
	flagFoldLines   int
	flagFoldOnStart bool

	// core
	flagProfile string
	flagTopic   string
	flagModel   string
	flagTTS     bool

	// chat
	flagStrategy      string
	flagContextLimit  int
	flagHistoryWindow int
	flagInputFile     string
	flagSkipHistory   bool
	flagNoStream      bool
	flagQuiet         bool
	flagDebug         bool
	flagAllProfiles   bool
	flagJSON          bool
	flagForce         bool

	// display
	flagSize     int
	flagHelpNoun string
	flagHelp     bool
	flagVersion  bool

	// admin — read/display
	flagTopicList    bool
	flagTopicInfo    bool
	flagTopicHistory bool
	flagTopicSummary bool
	flagSystem       bool
	flagConfig       bool
	flagProfileList  bool
	flagStats        bool
	flagStatus       bool

	// admin — write/mutate
	flagTopicNew          string
	flagTopicDelete       bool
	flagTopicClear        bool
	flagSystemSet         string
	flagSystemFile        string
	flagProfileDefaultSet string
	flagTopicDefaultSet   string
	flagTopicResource     string
	flagResourceList      bool
	flagResourceRemove    string
	flagNote              string
	flagDeleteLast        int
)

func init() {
	flag.CommandLine.SetOutput(os.Stdout)

	// mode
	flag.BoolVar(&flagNoTUI, "no-tui", false, "run in headless mode (no TUI)")
	flag.BoolVar(&flagNoTUI, "nw", false, "headless mode (short for --no-tui)")
	flag.BoolVar(&flagTextMode, "text-mode", false, "disable voice pipeline, text input only")
	flag.StringVar(&flagTheme, "theme", "auto", "color theme: light|dark|auto")
	flag.BoolVar(&flagChatLabels, "chat-labels", true, "prefix each turn with [you]: / [profile]:")
	flag.IntVar(&flagFoldLines, "fold-lines", 20, "lines threshold before folding entries (0 = never fold)")
	flag.BoolVar(&flagFoldOnStart, "fold-on-start", false, "start with all long entries folded")

	// core
	flag.StringVar(&flagProfile, "profile", "", "provider profile code")
	flag.StringVar(&flagProfile, "p", "", "provider profile code")
	flag.StringVar(&flagTopic, "topic", "", "topic name")
	flag.StringVar(&flagTopic, "t", "", "topic name")
	flag.StringVar(&flagModel, "model", "", "model name override (within active profile)")
	flag.StringVar(&flagModel, "m", "", "model name override (within active profile)")
	flag.BoolVar(&flagTTS, "tts", false, "speak the response aloud via say(1) after completion")

	// chat
	flag.StringVar(&flagStrategy, "strategy", "", "context strategy: tail|token-budget|summarize")
	flag.IntVar(&flagContextLimit, "context-limit", 0, "token budget override for this invocation")
	flag.IntVar(&flagHistoryWindow, "history-window", 0, "tail strategy: number of past user turns")
	flag.StringVar(&flagInputFile, "input-file", "", "read prompt from a text file")
	flag.StringVar(&flagInputFile, "i", "", "read prompt from a text file")
	flag.BoolVar(&flagSkipHistory, "skip-history", false, "do not persist this exchange to history")
	flag.BoolVar(&flagSkipHistory, "X", false, "do not persist this exchange to history")
	flag.BoolVar(&flagNoStream, "no-stream", false, "disable streaming; print full response at once")
	flag.BoolVar(&flagNoStream, "N", false, "disable streaming")
	flag.BoolVar(&flagQuiet, "quiet", false, "suppress warnings and stats on stderr")
	flag.BoolVar(&flagQuiet, "q", false, "suppress warnings and stats on stderr")
	flag.BoolVar(&flagDebug, "debug", false, "print request/response debug info to stderr")
	flag.BoolVar(&flagDebug, "D", false, "print request/response debug info to stderr")
	flag.BoolVar(&flagAllProfiles, "all-profiles", false, "run prompt against all configured profiles")
	flag.BoolVar(&flagAllProfiles, "A", false, "run prompt against all configured profiles")
	flag.BoolVar(&flagJSON, "json", false, "output result as JSON")
	flag.BoolVar(&flagForce, "force", false, "skip confirmation prompts")
	flag.BoolVar(&flagForce, "f", false, "skip confirmation prompts")

	// display / help
	flag.IntVar(&flagSize, "size", 20, "exchanges/lines to show for topic-history/topic-summary")
	flag.StringVar(&flagHelpNoun, "help-for", "", "show help for a command group: topic|resource|profile|system|session|info|files|all")
	flag.BoolVar(&flagHelp, "h", false, "show help (alias for --help-for all)")
	flag.BoolVar(&flagVersion, "version", false, "print version and exit")
	flag.BoolVar(&flagVersion, "v", false, "print version and exit")

	// admin — read/display
	flag.BoolVar(&flagTopicList, "topic-list", false, "list all topics")
	flag.BoolVar(&flagTopicList, "T", false, "list all topics")
	flag.BoolVar(&flagTopicInfo, "topic-info", false, "show topic contents")
	flag.BoolVar(&flagTopicHistory, "topic-history", false, "print last N exchanges from history")
	flag.BoolVar(&flagTopicSummary, "topic-summary", false, "print current summary for topic")
	flag.BoolVar(&flagSystem, "system", false, "print system prompt for topic")
	flag.BoolVar(&flagConfig, "config", false, "print resolved configuration")
	flag.BoolVar(&flagProfileList, "profile-list", false, "print configured profiles")
	flag.BoolVar(&flagStats, "stats", false, "print cumulative usage and cost stats")
	flag.BoolVar(&flagStatus, "status", false, "show effective defaults for next invocation")

	// admin — write/mutate
	flag.StringVar(&flagTopicNew, "topic-new", "", "create a new topic")
	flag.BoolVar(&flagTopicDelete, "topic-delete", false, "delete topic and all its files")
	flag.BoolVar(&flagTopicClear, "topic-clear", false, "erase history for topic")
	flag.StringVar(&flagSystemSet, "system-set", "", "set system prompt for topic")
	flag.StringVar(&flagSystemSet, "s", "", "set system prompt for topic")
	flag.StringVar(&flagSystemFile, "system-file", "", "set system prompt from file")
	flag.StringVar(&flagSystemFile, "S", "", "set system prompt from file")
	flag.StringVar(&flagProfileDefaultSet, "profile-default-set", "", "persist default profile to config")
	flag.StringVar(&flagProfileDefaultSet, "P", "", "persist default profile to config")
	flag.StringVar(&flagTopicDefaultSet, "topic-default-set", "", "persist default topic to config")
	flag.StringVar(&flagTopicResource, "topic-resource", "", "copy file into topic resources/")
	flag.StringVar(&flagTopicResource, "u", "", "copy file into topic resources/")
	flag.BoolVar(&flagResourceList, "resource-list", false, "list resources for topic")
	flag.StringVar(&flagResourceRemove, "resource-remove", "", "delete a named resource from topic")
	flag.StringVar(&flagNote, "note", "", "save a personal note to topic history (not sent to LLM)")
	flag.IntVar(&flagDeleteLast, "delete-last", -1, "delete last N exchanges (default 1) from topic history")
}

// resolveDisplaySettings merges config defaults with explicit flag overrides.
func resolveDisplaySettings(cfg config.Config) (chatLabels bool, foldLines int, foldOnStart bool) {
	chatLabels = true
	if cfg.ChatLabels != nil {
		chatLabels = *cfg.ChatLabels
	}
	foldLines = 20
	if cfg.FoldLines > 0 {
		foldLines = cfg.FoldLines
	}
	foldOnStart = false
	if cfg.FoldOnStart != nil {
		foldOnStart = *cfg.FoldOnStart
	}
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "chat-labels":
			chatLabels = flagChatLabels
		case "fold-lines":
			foldLines = flagFoldLines
		case "fold-on-start":
			foldOnStart = flagFoldOnStart
		}
	})
	return
}

// checkTerminal verifies the TUI is running in an interactive terminal.
func checkTerminal() error {
	if !isatty.IsTerminal(os.Stdout.Fd()) && !isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		return errors.New("stdout is not a terminal — use --nw for headless mode")
	}
	return nil
}

func main() {
	flag.Parse()
	os.Exit(run())
}

func c2Data() string {
	if h := os.Getenv("C2_DATA"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".c2"
	}
	return filepath.Join(home, ".c2")
}

func run() int {
	// Point lore's config package at ~/.c2 instead of ~/.lore.
	os.Setenv("LORE_DATA", c2Data())
	loreData := config.LoreData()
	bootstrapped, err := config.Bootstrap(loreData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: %v\n", err)
		return 1
	}
	if bootstrapped {
		return 0
	}
	cfgPath := config.DefaultConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	if flagHistoryWindow > 0 {
		cfg.WindowMessages = flagHistoryWindow
	}

	if flagVersion {
		fmt.Printf("c2 %s\n", version)
		return 0
	}

	if flagHelp {
		flag.Usage()
		return 0
	}

	chatLabels, foldLines, foldOnStart := resolveDisplaySettings(cfg)

	if isHeadless() {
		return runHeadless(cfg, cfgPath, chatLabels)
	}

	if err := checkTerminal(); err != nil {
		fmt.Fprintf(os.Stderr, "c2: %v\n", err)
		return 1
	}

	c2cfg, err := c2config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "c2config: %v\n", err)
		return 1
	}

	topicName := config.EffectiveTopic(cfg, flagTopic)
	e, err := engine.New(cfg, cfgPath, loreData, topicName, flagProfile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "engine: %v\n", err)
		return 1
	}
	if flagModel != "" {
		if err := e.OverrideModel(flagModel); err != nil {
			fmt.Fprintf(os.Stderr, "model override: %v\n", err)
			return 1
		}
	}
	if err := tui.Start(e, cfg, loreData, c2cfg, flagTheme, chatLabels, foldLines, foldOnStart, flagTextMode); err != nil {
		fmt.Fprintf(os.Stderr, "tui: %v\n", err)
		return 1
	}
	return 0
}

// isHeadless returns true when c2 should skip the TUI.
func isHeadless() bool {
	if flagNoTUI {
		return true
	}
	if flagTopicList || flagTopicInfo || flagTopicHistory || flagTopicSummary ||
		flagSystem || flagConfig || flagProfileList || flagStats ||
		flagStatus || flagTopicNew != "" || flagTopicDelete || flagTopicClear ||
		flagSystemSet != "" || flagSystemFile != "" || flagProfileDefaultSet != "" ||
		flagTopicDefaultSet != "" || flagTopicResource != "" || flagResourceList ||
		flagResourceRemove != "" || flagNote != "" ||
		flagDeleteLast >= 0 || flagAllProfiles || flagHelpNoun != "" || flagHelp {
		return true
	}
	return stdinIsPipe()
}

func stdinIsPipe() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice == 0
}
