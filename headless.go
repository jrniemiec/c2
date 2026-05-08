package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jrniemiec/c2/config"
	"github.com/jrniemiec/c2/core"
	"github.com/jrniemiec/c2/engine"
	"github.com/jrniemiec/c2/store"
)

func runHeadless(cfg config.Config, cfgPath string, chatLabels bool) int {
	loreData := config.LoreData()
	st := store.New(cfg.TopicsRoot)
	topicName := config.EffectiveTopic(cfg, flagTopic)

	// --- provider-independent admin commands (no engine/API key needed) ---

	if flagHelpNoun != "" {
		return cmdHelpNoun(flagHelpNoun)
	}
	if flagTopicList {
		return cmdListTopics(st)
	}
	if flagConfig {
		return cmdShowConfig(cfg)
	}
	if flagStatus {
		return cmdStatus(cfg, cfgPath, topicName, flagProfile)
	}
	if flagProfileList {
		return cmdShowProviders(cfg)
	}
	if flagStats {
		return cmdShowStats(loreData, topicName)
	}
	if flagTopicDefaultSet != "" {
		return cmdSetDefaultTopic(cfgPath, cfg, flagTopicDefaultSet)
	}
	if flagProfileDefaultSet != "" {
		return cmdSetDefaultProfile(cfgPath, cfg, flagProfileDefaultSet)
	}
	if flagTopicNew != "" {
		return cmdCreateTopic(st, flagTopicNew, flagSystemSet, flagSystemFile)
	}
	if flagTopicDelete {
		return cmdDeleteTopic(st, topicName, flagForce)
	}
	if flagTopicClear {
		return cmdClearHistory(st, topicName, flagForce)
	}
	if flagTopicHistory {
		return cmdShowHistory(st, topicName, flagSize)
	}
	if flagTopicSummary {
		return cmdShowSummary(st, topicName, flagSize)
	}
	if flagSystem {
		return cmdShowSystem(st, topicName)
	}
	if flagTopicInfo {
		return cmdShowTopic(st, topicName)
	}
	if flagSystemSet != "" || flagSystemFile != "" {
		return cmdSetSystem(st, topicName, flagSystemSet, flagSystemFile)
	}
	if flagTopicResource != "" {
		return cmdAddResource(st, topicName, flagTopicResource)
	}
	if flagResourceList {
		return cmdListResources(st, topicName)
	}
	if flagResourceRemove != "" {
		return cmdRemoveResource(st, topicName, flagResourceRemove, flagForce)
	}
	if flagNote != "" {
		return cmdAddNote(cfg, cfgPath, loreData, topicName, flagNote)
	}
	if flagDeleteLast >= 0 {
		n := flagDeleteLast
		if n == 0 {
			n = 1
		}
		return cmdDeleteLast(cfg, cfgPath, loreData, topicName, n, flagForce)
	}

	// --- chat (needs provider via engine) ---
	return runChat(cfg, cfgPath, loreData, topicName, chatLabels)
}

// =============================================================================
// Chat
// =============================================================================

func runChat(cfg config.Config, cfgPath, loreData, topicName string, chatLabels bool) int {
	prompt, err := resolvePrompt()
	if err != nil {
		fmt.Fprintf(os.Stderr, "prompt: %v\n", err)
		return 1
	}
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "usage: c2 [flags] <prompt>")
		flag.PrintDefaults()
		return 1
	}

	// Resolve @ref file injections before handing off to engine(s).
	resourceDir := core.TopicResourceDir(cfg.TopicsRoot, topicName)
	prompt, err = core.ResolveAtRefs(prompt, resourceDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	if flagAllProfiles {
		return runAllProfiles(cfg, cfgPath, loreData, topicName, prompt, chatLabels)
	}

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

	return doChat(e, prompt, chatLabels)
}

func runAllProfiles(cfg config.Config, cfgPath, loreData, topicName, prompt string, chatLabels bool) int {
	exitCode := 0
	for code := range cfg.Profiles {
		fmt.Fprintf(os.Stderr, "\n=== profile: %s ===\n", code)
		e, err := engine.New(cfg, cfgPath, loreData, topicName, code)
		if err != nil {
			fmt.Fprintf(os.Stderr, "engine: %v\n", err)
			exitCode = 1
			continue
		}
		if c := doChat(e, prompt, chatLabels); c != 0 {
			exitCode = c
		}
	}
	return exitCode
}

func doChat(e *engine.Engine, prompt string, chatLabels bool) int {
	opts := engine.ChatOptions{
		SkipHistory:      flagSkipHistory,
		NoStream:         flagNoStream || flagJSON,
		StrategyOverride: flagStrategy,
		BudgetOverride:   flagContextLimit,
		Debug:            flagDebug,
	}
	if !flagQuiet {
		opts.Out = os.Stderr
	}

	var (
		result   engine.ChatResult
		err      error
		response strings.Builder
	)

	if !flagQuiet {
		fmt.Fprintf(os.Stderr, "[topic: %s  model: %s]\n", e.TopicName(), e.Profile().Model)
	}

	if chatLabels {
		fmt.Printf("[you]: %s\n", prompt)
	}

	ctx := interruptContext()

	color := ""
	if !flagQuiet {
		color = ansiColor(e.Profile().Color)
	}

	if flagNoStream || flagJSON {
		// Collect full response then print.
		result, err = e.Chat(ctx, prompt, opts, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "chat: %v\n", err)
			return 1
		}
		// The response was persisted internally; we need it for JSON output.
		// Re-read from history (last assistant message).
		h := e.Topic().History
		if len(h.Msgs) > 0 {
			last := h.Msgs[len(h.Msgs)-1]
			if last.Role == core.RoleAssistant {
				response.WriteString(last.Content)
			}
		}
		if !flagJSON {
			if color != "" {
				fmt.Print(color)
			}
			if chatLabels {
				fmt.Printf("[%s]: ", e.ProfileCode())
			}
			fmt.Println(response.String())
			if color != "" {
				fmt.Print("\x1b[0m")
			}
		}
	} else {
		// Stream tokens directly to stdout.
		if color != "" {
			fmt.Print(color)
		}
		if chatLabels {
			fmt.Printf("[%s]: ", e.ProfileCode())
		}
		onDelta := func(delta string) error {
			fmt.Print(delta)
			return nil
		}
		result, err = e.Chat(ctx, prompt, opts, onDelta)
		if color != "" {
			fmt.Print("\x1b[0m")
		}
		fmt.Println() // newline after streamed output
		if err != nil {
			fmt.Fprintf(os.Stderr, "chat: %v\n", err)
			return 1
		}
	}

	if flagJSON {
		return printChatJSON(response.String(), result, e.Profile())
	}

	if !flagQuiet {
		printStats(result, e.Profile())
	}

	if flagTTS {
		h := e.Topic().History
		if len(h.Msgs) > 0 {
			last := h.Msgs[len(h.Msgs)-1]
			if last.Role == core.RoleAssistant {
				sayText(last.Content)
			}
		}
	}

	return 0
}

// sayText pipes text through say(1), blocking until playback completes.
func sayText(text string) {
	cmd := exec.Command("say", "-r", "200")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = strings.NewReader(core.TTSStrip(text))

	// Kill the process group on SIGINT so say doesn't outlive the parent.
	done := make(chan struct{})
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		select {
		case <-c:
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
		signal.Stop(c)
	}()

	_ = cmd.Run()
	close(done)
}

func printChatJSON(response string, result engine.ChatResult, profile config.ProviderProfile) int {
	inPer1M, outPer1M, hasPricing := config.ExtractPricing(profile.Info)
	var cost float64
	if hasPricing {
		cost = config.CalcCost(result.Usage.InputTokens, result.Usage.OutputTokens, inPer1M, outPer1M)
	}
	out := map[string]any{
		"response": response,
		"usage": map[string]any{
			"input_tokens":  result.Usage.InputTokens,
			"output_tokens": result.Usage.OutputTokens,
			"estimated":     result.Usage.Estimated,
		},
		"elapsed_ms": result.Elapsed.Milliseconds(),
	}
	if hasPricing {
		out["cost_usd"] = cost
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return 0
}

func printStats(result engine.ChatResult, profile config.ProviderProfile) {
	u := result.Usage
	s := fmt.Sprintf("[%d in / %d out tokens", u.InputTokens, u.OutputTokens)
	inPer1M, outPer1M, hasPricing := config.ExtractPricing(profile.Info)
	if hasPricing {
		cost := config.CalcCost(u.InputTokens, u.OutputTokens, inPer1M, outPer1M)
		s += " | " + config.FormatCost(cost)
	}
	s += fmt.Sprintf(" | %dms]", result.Elapsed.Milliseconds())
	fmt.Fprintln(os.Stderr, s)
}

// =============================================================================
// Admin commands
// =============================================================================

func cmdListTopics(st *store.FileStore) int {
	topics, err := st.ListTopics()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list topics: %v\n", err)
		return 1
	}
	if len(topics) == 0 {
		fmt.Fprintln(os.Stderr, "(no topics)")
		return 0
	}
	for _, t := range topics {
		fmt.Println(t)
	}
	return 0
}

func cmdShowConfig(cfg config.Config) int {
	b, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(b))
	return 0
}

func cmdStatus(cfg config.Config, cfgPath, topicName, profileCode string) int {
	resolvedCode, profile, err := config.ResolveProfile(cfg, profileCode)
	stratName := "(unknown)"
	if err == nil {
		stratName = resolveStatusStrategy(profile)
	}
	fmt.Printf("config:   %s\n", cfgPath)
	fmt.Printf("topics:   %s\n", cfg.TopicsRoot)
	fmt.Printf("topic:    %s\n", topicName)
	if err == nil {
		fmt.Printf("profile:  %s (%s/%s)\n", resolvedCode, profile.Provider, profile.Model)
		fmt.Printf("strategy: %s\n", stratName)
	} else {
		fmt.Printf("profile:  (none) — %v\n", err)
	}
	return 0
}

func resolveStatusStrategy(p config.ProviderProfile) string {
	if p.Strategy != "" {
		return p.Strategy
	}
	if p.SummarizerProfile != "" {
		return "summarize"
	}
	if p.ContextTokenLimit > 0 || p.MaxContextTokens > 0 {
		return "token-budget"
	}
	return "tail"
}

func cmdShowProviders(cfg config.Config) int {
	for code, p := range cfg.Profiles {
		strat := resolveStatusStrategy(p)
		line := fmt.Sprintf("%-16s  %-12s  %-32s  strategy: %s", code, p.Provider, p.Model, strat)
		if p.MaxContextTokens > 0 {
			line += fmt.Sprintf("  context: %s", core.FormatTokens(p.MaxContextTokens))
		}
		inPer1M, outPer1M, ok := config.ExtractPricing(p.Info)
		if ok {
			line += fmt.Sprintf("  $%.2f/$%.2f per 1M", inPer1M, outPer1M)
		}
		fmt.Println(line)
	}
	return 0
}

func cmdShowStats(loreData, topicFilter string) int {
	logPath := store.UsageLogPath(loreData)
	entries, err := store.ReadUsageLog(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read usage log: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "(no usage recorded)")
		return 0
	}
	agg := store.AggregateUsage(entries, topicFilter, 0)
	fmt.Printf("total calls:   %d\n", agg.Total.Calls)
	fmt.Printf("input tokens:  %s\n", core.FormatTokens(agg.Total.InputTokens))
	fmt.Printf("output tokens: %s\n", core.FormatTokens(agg.Total.OutputTokens))
	if agg.Total.CostUSD > 0 {
		fmt.Printf("cost:          %s\n", config.FormatCost(agg.Total.CostUSD))
	}
	if len(agg.ByProfile) > 1 {
		fmt.Println("\nby profile:")
		for code, s := range agg.ByProfile {
			fmt.Printf("  %-16s  %d calls  %s in  %s out", code, s.Calls,
				core.FormatTokens(s.InputTokens), core.FormatTokens(s.OutputTokens))
			if s.CostUSD > 0 {
				fmt.Printf("  %s", config.FormatCost(s.CostUSD))
			}
			fmt.Println()
		}
	}
	return 0
}

func cmdSetDefaultTopic(cfgPath string, cfg config.Config, name string) int {
	cfg.DefaultTopic = name
	if err := config.SaveAtomic(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "save config: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "default topic set to %q\n", name)
	return 0
}

func cmdSetDefaultProfile(cfgPath string, cfg config.Config, code string) int {
	if _, _, err := config.ResolveProfile(cfg, code); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	cfg.DefaultProfile = code
	if err := config.SaveAtomic(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "save config: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "default profile set to %q\n", code)
	return 0
}

func cmdCreateTopic(st *store.FileStore, name, system, systemFile string) int {
	if _, err := st.LoadTopic(name); err != nil {
		fmt.Fprintf(os.Stderr, "create topic: %v\n", err)
		return 1
	}
	text, ok := resolveSystemText(system, systemFile)
	if !ok {
		return 1
	}
	if text != "" {
		if err := st.SaveSystem(name, text); err != nil {
			fmt.Fprintf(os.Stderr, "set system: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(os.Stderr, "topic %q created\n", name)
	return 0
}

func cmdDeleteTopic(st *store.FileStore, topicName string, force bool) int {
	if !force && !confirm(fmt.Sprintf("Delete topic %q and all its files?", topicName)) {
		fmt.Fprintln(os.Stderr, "aborted")
		return 0
	}
	if err := st.DeleteTopic(topicName); err != nil {
		fmt.Fprintf(os.Stderr, "delete topic: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "topic %q deleted\n", topicName)
	return 0
}

func cmdClearHistory(st *store.FileStore, topicName string, force bool) int {
	if !force && !confirm(fmt.Sprintf("Clear history for topic %q?", topicName)) {
		fmt.Fprintln(os.Stderr, "aborted")
		return 0
	}
	if err := st.ClearHistory(topicName); err != nil {
		fmt.Fprintf(os.Stderr, "clear history: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "history cleared for topic %q\n", topicName)
	return 0
}

func cmdShowHistory(st *store.FileStore, topicName string, n int) int {
	h, err := st.LoadHistory(topicName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load history: %v\n", err)
		return 1
	}
	if len(h.Msgs) == 0 {
		fmt.Fprintln(os.Stderr, "(no history)")
		return 0
	}
	// Collect exchange pairs (user + assistant).
	type pair struct{ user, assistant core.Message }
	var pairs []pair
	for i := 0; i < len(h.Msgs)-1; i++ {
		if h.Msgs[i].Role == core.RoleUser && h.Msgs[i+1].Role == core.RoleAssistant {
			pairs = append(pairs, pair{h.Msgs[i], h.Msgs[i+1]})
			i++ // skip assistant message
		}
	}
	if n > 0 && len(pairs) > n {
		pairs = pairs[len(pairs)-n:]
	}
	for i, p := range pairs {
		if i > 0 {
			fmt.Println("---")
		}
		fmt.Printf("you · %s\n%s\n\n", p.user.Time.Format("15:04"), p.user.Content)
		fmt.Printf("c2 · %s\n%s\n", p.assistant.Time.Format("15:04"), p.assistant.Content)
		fmt.Println()
	}
	return 0
}

func cmdShowSummary(st *store.FileStore, topicName string, n int) int {
	text, coversThrough, err := st.LoadSummary(topicName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load summary: %v\n", err)
		return 1
	}
	if text == "" {
		fmt.Fprintln(os.Stderr, "(no summary)")
		return 0
	}
	fmt.Fprintf(os.Stderr, "(covers through message %d)\n", coversThrough+1)
	lines := strings.Split(text, "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	fmt.Println(strings.Join(lines, "\n"))
	return 0
}

func cmdShowSystem(st *store.FileStore, topicName string) int {
	text, err := st.LoadSystem(topicName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load system: %v\n", err)
		return 1
	}
	if text == "" {
		fmt.Fprintln(os.Stderr, "(no system prompt)")
		return 0
	}
	fmt.Print(text)
	if !strings.HasSuffix(text, "\n") {
		fmt.Println()
	}
	return 0
}

func cmdShowTopic(st *store.FileStore, topicName string) int {
	h, err := st.LoadHistory(topicName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load history: %v\n", err)
		return 1
	}
	sys, _ := st.LoadSystem(topicName)
	summText, coversThrough, _ := st.LoadSummary(topicName)

	fmt.Printf("topic:   %s\n", topicName)
	fmt.Printf("system:  %s\n", yesNo(sys != ""))
	fmt.Printf("history: %d messages (~%s tokens)\n", len(h.Msgs), core.FormatTokens(totalHistoryTokens(h)))
	if summText != "" {
		fmt.Printf("summary: covers messages 1-%d (~%s tokens)\n",
			coversThrough+1, core.FormatTokens(core.ApproxTokens(summText)))
	} else {
		fmt.Println("summary: (none)")
	}
	return 0
}

func cmdSetSystem(st *store.FileStore, topicName, system, systemFile string) int {
	text, ok := resolveSystemText(system, systemFile)
	if !ok {
		return 1
	}
	if err := st.SaveSystem(topicName, text); err != nil {
		fmt.Fprintf(os.Stderr, "set system: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "system prompt set for topic %q\n", topicName)
	return 0
}

func cmdDeleteLast(cfg config.Config, cfgPath, loreData, topicName string, n int, force bool) int {
	noun := "exchange"
	if n > 1 {
		noun = fmt.Sprintf("%d exchanges", n)
	}
	if !force && !confirm(fmt.Sprintf("Delete last %s from topic %q?", noun, topicName)) {
		fmt.Fprintln(os.Stderr, "aborted")
		return 0
	}
	eng, err := engine.New(cfg, cfgPath, loreData, topicName, flagProfile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "engine: %v\n", err)
		return 1
	}
	removed, err := eng.DeleteLast(n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "delete-last: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "deleted %d exchange(s) from topic %q\n", removed, topicName)
	return 0
}

func cmdAddNote(cfg config.Config, cfgPath, loreData, topicName, text string) int {
	eng, err := engine.New(cfg, cfgPath, loreData, topicName, flagProfile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "engine: %v\n", err)
		return 1
	}
	if err := eng.AddNote(text); err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "note saved to topic %q\n", topicName)
	return 0
}

func cmdAddResource(st *store.FileStore, topicName, sourcePath string) int {
	if err := st.CreateResource(topicName, sourcePath); err != nil {
		fmt.Fprintf(os.Stderr, "add resource: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "resource added to topic %q\n", topicName)
	return 0
}

func cmdListResources(st *store.FileStore, topicName string) int {
	files, err := st.ListResources(topicName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list resources: %v\n", err)
		return 1
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "(no resources for topic %q)\n", topicName)
		return 0
	}
	for _, fi := range files {
		size := fi.Size()
		var sizeStr string
		switch {
		case size >= 1024*1024:
			sizeStr = fmt.Sprintf("%.1f MB", float64(size)/1024/1024)
		case size >= 1024:
			sizeStr = fmt.Sprintf("%.1f KB", float64(size)/1024)
		default:
			sizeStr = fmt.Sprintf("%d B", size)
		}
		fmt.Printf("%-32s  %8s  %s\n", fi.Name(), sizeStr, fi.ModTime().Format("2006-01-02 15:04:05"))
	}
	return 0
}

func cmdRemoveResource(st *store.FileStore, topicName, name string, force bool) int {
	if !force {
		fmt.Fprintf(os.Stderr, "remove resource %q from topic %q? [yes/N]: ", name, topicName)
		var ans string
		fmt.Fscan(os.Stdin, &ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "yes" {
			fmt.Fprintln(os.Stderr, "cancelled")
			return 0
		}
	}
	if err := st.DeleteResource(topicName, name); err != nil {
		fmt.Fprintf(os.Stderr, "remove resource: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "resource %q removed from topic %q\n", name, topicName)
	return 0
}

// =============================================================================
// Helpers
// =============================================================================

// resolvePrompt reads the prompt from flags, stdin, or positional args.
// Positional args become the instruction; piped stdin is appended as payload.
func resolvePrompt() (string, error) {
	if flagInputFile != "" {
		b, err := os.ReadFile(flagInputFile)
		if err != nil {
			return "", fmt.Errorf("read input file: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}

	positional := strings.TrimSpace(strings.Join(flag.Args(), " "))

	if stdinIsPipe() {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		piped := strings.TrimSpace(string(b))
		if positional != "" && piped != "" {
			return positional + "\n" + piped, nil
		}
		return positional + piped, nil
	}

	return positional, nil
}

// resolveSystemText returns the system prompt text from inline string or file.
func resolveSystemText(inline, filePath string) (string, bool) {
	if filePath != "" {
		b, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read system file: %v\n", err)
			return "", false
		}
		return string(b), true
	}
	return inline, true
}

// confirm prompts the user for y/n confirmation on stderr.
func confirm(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		return strings.ToLower(strings.TrimSpace(sc.Text())) == "y"
	}
	return false
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func totalHistoryTokens(h *core.History) int {
	total := 0
	for _, m := range h.Msgs {
		total += core.ApproxTokens(m.Content)
	}
	return total
}

func cmdHelpNoun(noun string) int {
	groups := map[string][]helpEntry{
		"topic": {
			{"--topic-list", "list all topics with message count and last used"},
			{"--topic-info", "show info for current topic (history, system, summary)"},
			{"--topic-history [--size N]", "show last N exchanges (default 20)"},
			{"--topic-summary", "show current context summary for topic"},
			{"--topic-new <name>", "create a new topic and switch to it"},
			{"--topic-delete [--force]", "delete current topic and all its files"},
			{"--topic-clear [--force]", "erase history for current topic"},
			{"--topic-default-set <name>", "persist default topic to config"},
		},
		"resource": {
			{"--resource-list", "list resources for current topic"},
			{"--resource-add <file> / -u", "copy file into topic resources"},
			{"--resource-remove <name> [--force]", "delete a named resource from topic"},
		},
		"profile": {
			{"--profile-list", "list all configured profiles"},
			{"--profile-default-set <name>", "persist default profile to config"},
		},
		"system": {
			{"--system", "show system prompt for current topic"},
			{"--system-set <text> / -s", "set system prompt inline"},
			{"--system-file <path> / -S", "set system prompt from file"},
		},
		"session": {
			{"--model <name> / -m", "override model name within active profile"},
			{"--strategy <name>", "override context strategy: tail|token-budget|summarize"},
			{"--context-limit <n>", "override token budget for this invocation"},
			{"--history-window <n>", "override tail window (number of past user turns)"},
			{"--skip-history / -X", "do not persist this exchange to history"},
			{"--no-stream / -N", "disable streaming; print full response at once"},
			{"--all-profiles / -A", "run prompt against all configured profiles"},
			{"--tts", "speak the response aloud via say(1) after completion"},
			{"--json", "output result as JSON"},
			{"--quiet / -q", "suppress topic/model header and stats"},
			{"--force / -f", "skip confirmation prompts"},
			{"--note <text>", "save a personal note to topic history (not sent to LLM)"},
			{"--delete-last [n]", "delete last N exchanges from topic history (default 1)"},
		},
		"info": {
			{"--config", "print resolved configuration"},
			{"--status", "show effective defaults for next invocation"},
			{"--stats", "print cumulative usage and cost stats"},
			{"--debug / -D", "print request/response debug info to stderr"},
			{"--help-for <noun>", "show help for a command group"},
		},
	}
	filesLines := []string{
		"files:",
		"  Embed one or more @ref tokens in any prompt to inject file content.",
		"  The surrounding text becomes the instruction; each file becomes a block.",
		"  All refs must resolve or the request is aborted.",
		"",
		"  @name               topic resources folder  (resources/name)",
		"  @subdir/name        topic resources folder  (resources/subdir/name)",
		"  @./path  @../path   relative filesystem path (from current directory)",
		"  @/absolute/path     absolute filesystem path",
		"  @~/path             home-relative filesystem path",
		"",
		"  Examples:",
		"    lore 'explain this' @main.go",
		"    lore 'compare @old.py and @new.py and list the differences'",
		"    lore 'summarize @notes.txt and cross-check with @~/docs/spec.md'",
		"",
	}
	order := []string{"topic", "resource", "profile", "system", "session", "info", "files"}

	noun = strings.ToLower(strings.TrimSpace(noun))
	if noun == "all" || noun == "" {
		for _, n := range order {
			if n == "files" {
				for _, l := range filesLines {
					fmt.Println(l)
				}
			} else {
				printHelpGroup(n, groups[n])
			}
		}
		return 0
	}
	if noun == "files" {
		for _, l := range filesLines {
			fmt.Println(l)
		}
		return 0
	}
	entries, ok := groups[noun]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown noun %q — available: %s\n", noun, strings.Join(order, "|"))
		return 1
	}
	printHelpGroup(noun, entries)
	return 0
}

type helpEntry struct {
	flag string
	desc string
}

func printHelpGroup(noun string, entries []helpEntry) {
	fmt.Printf("%s:\n", noun)
	for _, e := range entries {
		fmt.Printf("  %-38s  %s\n", e.flag, e.desc)
	}
	fmt.Println()
}

// ansiColor maps a color name to an ANSI foreground escape sequence.
func ansiColor(name string) string {
	switch name {
	case "black":
		return "\x1b[30m"
	case "red":
		return "\x1b[31m"
	case "green":
		return "\x1b[32m"
	case "yellow":
		return "\x1b[33m"
	case "blue":
		return "\x1b[34m"
	case "magenta":
		return "\x1b[35m"
	case "cyan":
		return "\x1b[36m"
	case "white":
		return "\x1b[37m"
	case "bright_black", "gray", "grey":
		return "\x1b[90m"
	case "bright_red":
		return "\x1b[91m"
	case "bright_green":
		return "\x1b[92m"
	case "bright_yellow":
		return "\x1b[93m"
	case "bright_blue":
		return "\x1b[94m"
	case "bright_magenta":
		return "\x1b[95m"
	case "bright_cyan":
		return "\x1b[96m"
	case "bright_white":
		return "\x1b[97m"
	default:
		return ""
	}
}

// interruptContext returns a context that is cancelled on SIGINT.
func interruptContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		cancel()
	}()
	return ctx
}
