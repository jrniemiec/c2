package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jrniemiec/c2/config"
	"github.com/jrniemiec/c2/core"
	"github.com/jrniemiec/c2/store"
)

// knownCommands is the set of command names (without leading /) for bare-word recognition.
var knownCommands = map[string]bool{
	"exit": true, "help": true, "delete-last": true, "fold": true, "unfold": true, "play-all": true,
	"topic": true, "topic-switch": true, "topic-new": true, "topic-list": true,
	"topic-delete": true, "topic-clear": true, "topic-default": true,
	"topic-default-set": true, "topic-summary": true, "topic-history": true,
	"topic-resource": true,
	"resource-list": true, "resource-add": true, "resource-new": true, "resource-remove": true, "resource-delete": true, "resource-view": true, "resource-edit": true,
	"tts": true, "voice-commands": true, "export": true,
	"profile": true, "profile-switch": true, "profile-list": true,
	"profile-default": true, "profile-default-set": true,
	"system": true, "system-set": true, "system-clear": true,
	"config": true, "status": true, "stats": true,
	"theme": true, "block-keys": true,
	"log": true,
}

// looksLikeCommand returns true if the input (no leading /) has ≤ 2 words and
// the first word matches a known command name.
func looksLikeCommand(val string) bool {
	fields := strings.Fields(val)
	if len(fields) == 0 || len(fields) > 2 {
		return false
	}
	return knownCommands[strings.ToLower(fields[0])]
}

// handleCommand parses and executes a slash command, returning the result for
// display in the bottom pane. The input string includes the leading '/'.
func handleCommand(m *Model, input string) cmdResult {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return errResult(input, "empty command")
	}
	cmd := strings.ToLower(fields[0])
	args := fields[1:]

	switch cmd {
	// --- topic ---
	case "/topic":
		return cmdTopicInfo(m, args)
	case "/topic-switch":
		return cmdTopicSwitch(m, args)
	case "/topic-new":
		return cmdTopicNew(m, args)
	case "/topic-list":
		return cmdTopicList(m)
	case "/topic-delete":
		return cmdTopicDelete(m, args)
	case "/topic-clear":
		return cmdTopicClear(m, args)
	case "/topic-default":
		return cmdTopicDefault(m)
	case "/topic-default-set":
		return cmdTopicDefaultSet(m, args)
	case "/topic-summary":
		return cmdTopicSummary(m)
	case "/topic-history":
		return cmdTopicHistory(m, args)
	case "/topic-resource", "/resource-add":
		return cmdResourceAdd(m, args)
	case "/resource-list":
		return cmdResourceList(m, args)
	case "/resource-remove", "/resource-delete":
		return cmdResourceRemove(m, args)
	case "/resource-view":
		return cmdResourceOpen(m, args)
	case "/resource-edit":
		return cmdResourceEdit(m, args)
	case "/resource-new":
		return cmdResourceNew(m, args)

	// --- profile ---
	case "/profile":
		return cmdProfileInfo(m, args)
	case "/profile-switch":
		return cmdProfileSwitch(m, args)
	case "/profile-list":
		return cmdProfileList(m, args)
	case "/profile-default":
		return cmdProfileDefault(m)
	case "/profile-default-set":
		return cmdProfileDefaultSet(m, args)

	// --- system ---
	case "/system":
		return cmdSystem(m, args)
	case "/system-set":
		return cmdSystemSet(m, args)
	case "/system-clear":
		return cmdSystemClear(m)

	// --- view ---
	case "/tts":
		return cmdTTS(m, args)
	case "/fold":
		return cmdFold(m)
	case "/unfold":
		return cmdUnfold(m)
	case "/play-all":
		return cmdPlayAll(m)

	// --- nav ---
	case "/block-keys":
		return cmdBlockKeys()

	// --- theme ---
	case "/theme":
		return cmdTheme(m, args)

	// --- info ---
	case "/config":
		return cmdConfig(m)
	case "/status":
		return cmdStatus(m)
	case "/stats":
		return cmdStats(m)

	// --- help ---
	case "/delete-last":
		return cmdDeleteLast(m, args)
	case "/voice-commands":
		return cmdVoiceCommands()
	case "/export":
		return cmdExport(m, args)
	case "/log":
		return cmdLog(m)
	case "/help":
		return cmdHelp("/help", args)

	// --- exit ---
	case "/exit":
		return cmdResult{input: input, output: nil, isError: false, quit: true}

	default:
		return errResult(input, fmt.Sprintf("unknown command %q — type /help for a list", cmd))
	}
}

// =============================================================================
// topic commands
// =============================================================================

func cmdTopicInfo(m *Model, args []string) cmdResult {
	name := m.eng.TopicName()
	if len(args) > 0 {
		name = args[0]
	}
	h, err := m.eng.Topic().History, error(nil)
	if len(args) > 0 {
		// Load history for a different topic.
		st := store.New(m.cfg.TopicsRoot)
		loaded, e := st.LoadHistory(name)
		if e != nil {
			return errResult("/topic "+name, fmt.Sprintf("load topic: %v", e))
		}
		h = loaded
		err = e
	}
	_ = err
	sys := ""
	if len(args) == 0 {
		sys = m.eng.SystemPrompt()
	}
	lines := []string{
		fmt.Sprintf("topic:   %s", name),
		fmt.Sprintf("system:  %s", yesNoStr(sys != "")),
		fmt.Sprintf("history: %d messages (~%s tokens)", len(h.Msgs),
			core.FormatTokens(totalTokens(h))),
	}
	if len(args) == 0 {
		sumText, through, _ := m.eng.LoadSummary()
		if sumText != "" {
			lines = append(lines, fmt.Sprintf("summary: covers messages 1-%d", through+1))
		} else {
			lines = append(lines, "summary: (none)")
		}
	}
	return okResult("/topic "+name, lines)
}

func cmdTopicSwitch(m *Model, args []string) cmdResult {
	if len(args) == 0 {
		return errResult("/topic-switch", "usage: /topic-switch <name>")
	}
	name := args[0]
	if err := m.eng.SwitchTopic(name); err != nil {
		return errResult("/topic-switch "+name, err.Error())
	}
	m.exchanges = nil
	m.loadHistory()
	m.rebuildConvContent()
	return okResult("/topic-switch "+name, []string{fmt.Sprintf("switched to topic %q", name)})
}

func cmdTopicNew(m *Model, args []string) cmdResult {
	if len(args) == 0 {
		return errResult("/topic-new", "usage: /topic-new <name>")
	}
	name := args[0]
	if err := m.eng.CreateTopic(name, ""); err != nil {
		return errResult("/topic-new "+name, err.Error())
	}
	return okResult("/topic-new "+name, []string{fmt.Sprintf("topic %q created", name)})
}

func cmdTopicList(m *Model) cmdResult {
	topics, err := m.eng.ListTopics()
	if err != nil {
		return errResult("/topic-list", err.Error())
	}
	if len(topics) == 0 {
		r := okResult("/topic-list", []string{"(no topics)"})
		r.spokenText = "No topics."
		return r
	}
	cur := m.eng.TopicName()
	lines := make([]string, len(topics))
	for i, t := range topics {
		if t == cur {
			lines[i] = t + " ←"
		} else {
			lines[i] = t
		}
	}
	r := okResult("/topic-list", lines)
	r.spokenText = "Topics: " + strings.Join(topics, ", ") + ". Current: " + cur + "."
	return r
}

func cmdTopicDelete(m *Model, args []string) cmdResult {
	name := m.eng.TopicName()
	if len(args) > 0 {
		name = args[0]
	}
	// Verify topic exists before asking for confirmation.
	topics, err := m.eng.ListTopics()
	if err != nil {
		return errResult("/topic-delete "+name, err.Error())
	}
	found := false
	for _, t := range topics {
		if t == name {
			found = true
			break
		}
	}
	if !found {
		return errResult("/topic-delete "+name, fmt.Sprintf("topic %q not found", name))
	}
	// Register pending confirmation — executed on "yes" + Enter, cancelled by any other input.
	label := "/topic-delete " + name
	m.pendingAction = func() cmdResult {
		if err := m.eng.DeleteTopic(name); err != nil {
			return errResult(label, err.Error())
		}
		if name == m.eng.TopicName() {
			defaultName := config.EffectiveTopic(m.cfg, "")
			_ = m.eng.SwitchTopic(defaultName)
			m.exchanges = nil
			m.loadHistory()
			m.rebuildConvContent()
		}
		return okResult(label, []string{fmt.Sprintf("topic %q deleted", name)})
	}
	return okResult(label, []string{
		fmt.Sprintf("Topic %q and all its history will be permanently deleted.", name),
		"[yes] to confirm, other input or Esc to cancel:",
	})
}

func cmdTopicClear(m *Model, args []string) cmdResult {
	name := m.eng.TopicName()
	m.pendingAction = func() cmdResult {
		if err := m.eng.ClearHistory(); err != nil {
			return errResult("/topic-clear", err.Error())
		}
		m.exchanges = nil
		m.rebuildConvContent()
		return okResult("/topic-clear", []string{fmt.Sprintf("history cleared for topic %q", name)})
	}
	return okResult("/topic-clear", []string{
		fmt.Sprintf("All history for topic %q will be permanently deleted.", name),
		"[yes] to confirm, other input or Esc to cancel:",
	})
}

func cmdTopicDefault(m *Model) cmdResult {
	def := m.cfg.DefaultTopic
	if def == "" {
		def = "(not set)"
	}
	return okResult("/topic-default", []string{"default topic: " + def})
}

func cmdTopicDefaultSet(m *Model, args []string) cmdResult {
	if len(args) == 0 {
		return errResult("/topic-default-set", "usage: /topic-default-set <name>")
	}
	name := args[0]
	if err := m.eng.SetDefaultTopic(name); err != nil {
		return errResult("/topic-default-set "+name, err.Error())
	}
	m.cfg = m.eng.Config()
	return okResult("/topic-default-set "+name, []string{fmt.Sprintf("default topic set to %q", name)})
}

func cmdTopicSummary(m *Model) cmdResult {
	text, through, err := m.eng.LoadSummary()
	if err != nil {
		return errResult("/topic-summary", err.Error())
	}
	if text == "" {
		return okResult("/topic-summary", []string{"(no summary)"})
	}
	lines := []string{fmt.Sprintf("(covers through message %d)", through+1)}
	lines = append(lines, strings.Split(strings.TrimRight(text, "\n"), "\n")...)
	return okResult("/topic-summary", lines)
}

func cmdTopicHistory(m *Model, args []string) cmdResult {
	n := 10
	if len(args) > 0 {
		if v, err := strconv.Atoi(args[0]); err == nil && v > 0 {
			n = v
		}
	}
	h := m.eng.Topic().History
	type pair struct{ user, asst core.Message }
	var pairs []pair
	for i := 0; i+1 < len(h.Msgs); i++ {
		if h.Msgs[i].Role == core.RoleUser && h.Msgs[i+1].Role == core.RoleAssistant {
			pairs = append(pairs, pair{h.Msgs[i], h.Msgs[i+1]})
			i++
		}
	}
	if len(pairs) == 0 {
		return okResult("/topic-history", []string{"(no history)"})
	}
	if n > 0 && len(pairs) > n {
		pairs = pairs[len(pairs)-n:]
	}
	var lines []string
	for i, p := range pairs {
		if i > 0 {
			lines = append(lines, "---")
		}
		lines = append(lines, fmt.Sprintf("you · %s", p.user.Time.Format("15:04")))
		lines = append(lines, p.user.Content)
		lines = append(lines, fmt.Sprintf("c2 · %s", p.asst.Time.Format("15:04")))
		lines = append(lines, p.asst.Content)
	}
	return okResult("/topic-history", lines)
}

func cmdResourceAdd(m *Model, args []string) cmdResult {
	if len(args) == 0 {
		return errResult("/resource-add", "usage: /resource-add <file>")
	}
	path := args[0]
	if err := m.eng.AddResource(path); err != nil {
		return errResult("/resource-add "+path, err.Error())
	}
	return okResult("/resource-add "+path, []string{fmt.Sprintf("resource %q added to topic %q", path, m.eng.TopicName())})
}

func cmdResourceList(m *Model, args []string) cmdResult {
	topicName := m.eng.TopicName()
	if len(args) > 0 {
		topicName = args[0]
	}
	st := store.New(m.cfg.TopicsRoot)
	files, err := st.ListResources(topicName)
	if err != nil {
		return errResult("/resource-list", err.Error())
	}
	if len(files) == 0 {
		r := okResult("/resource-list", []string{fmt.Sprintf("(no resources for topic %q)", topicName)})
		r.spokenText = "No resources for topic " + topicName + "."
		return r
	}
	lines := []string{fmt.Sprintf("resources for topic %q:", topicName)}
	fileNames := make([]string, len(files))
	for i, fi := range files {
		fileNames[i] = fi.Name()
		size := fi.Size()
		var sizeStr string
		switch {
		case size >= 2024*1024:
			sizeStr = fmt.Sprintf("%.1f MB", float64(size)/1024/1024)
		case size >= 2024:
			sizeStr = fmt.Sprintf("%.1f KB", float64(size)/1024)
		default:
			sizeStr = fmt.Sprintf("%d B", size)
		}
		lines = append(lines, fmt.Sprintf("  %-32s  %8s  %s", fi.Name(), sizeStr, fi.ModTime().Format(time.DateTime)))
	}
	r := okResult("/resource-list", lines)
	r.spokenText = "Resources for topic " + topicName + ": " + strings.Join(fileNames, ", ") + "."
	return r
}

func cmdResourceRemove(m *Model, args []string) cmdResult {
	if len(args) == 0 {
		return errResult("/resource-remove", "usage: /resource-remove <name>")
	}
	name := args[0]
	topicName := m.eng.TopicName()
	label := "/resource-remove " + name
	m.pendingAction = func() cmdResult {
		if err := m.eng.RemoveResource(name); err != nil {
			return errResult(label, err.Error())
		}
		return okResult(label, []string{fmt.Sprintf("resource %q removed from topic %q", name, topicName)})
	}
	return okResult(label, []string{
		fmt.Sprintf("Resource %q will be permanently deleted from topic %q.", name, topicName),
		"[yes] to confirm, other input or Esc to cancel:",
	})
}

func cmdResourceOpen(m *Model, args []string) cmdResult {
	if len(args) == 0 {
		return errResult("/resource-view", "usage: /resource-view <name>")
	}
	name := args[0]
	filePath := filepath.Join(m.eng.ResourceDir(), name)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return errResult("/resource-view "+name, fmt.Sprintf("resource %q not found", name))
	}
	// Binary detection: non-UTF8 bytes in first 512 bytes.
	check := data
	if len(check) > 512 {
		check = check[:512]
	}
	if !utf8.Valid(check) {
		return errResult("/resource-view "+name, fmt.Sprintf("%q is not a text file", name))
	}
	// Truncate large files.
	const maxBytes = 200 * 1024
	truncated := false
	if len(data) > maxBytes {
		data = data[:maxBytes]
		truncated = true
	}
	text := string(data)
	if truncated {
		text += "\n[file truncated at 200 KB]"
	}
	// Open overlay.
	m.preFocus = m.focus
	m.preFocusedExIdx = m.focusedExIdx
	m.resourceLines = strings.Split(text, "\n")
	m.resourceName = name
	m.resourceCursor = 0
	m.setFocus(paneResource)
	return cmdResult{input: "/resource-view " + name, suppressCmdPane: true}
}

func cmdResourceEdit(m *Model, args []string) cmdResult {
	if len(args) == 0 {
		return errResult("/resource-edit", "usage: /resource-edit <name>")
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return errResult("/resource-edit", "$EDITOR is not set or not exported — add 'export EDITOR=<path>' to your shell config")
	}
	name := args[0]
	filePath := filepath.Join(m.eng.ResourceDir(), name)
	if _, err := os.Stat(filePath); err != nil {
		return errResult("/resource-edit "+name, fmt.Sprintf("resource %q not found", name))
	}
	editorCmd := exec.Command(editor, filePath)
	res := cmdResult{
		input:           "/resource-edit " + name,
		suppressCmdPane: true,
		execCmd: tea.ExecProcess(editorCmd, func(err error) tea.Msg {
			return resourceReloadMsg{name: name}
		}),
	}
	return res
}

func cmdResourceNew(m *Model, args []string) cmdResult {
	if len(args) == 0 {
		return errResult("/resource-new", "usage: /resource-new <name>")
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return errResult("/resource-new", "$EDITOR is not set — add 'export EDITOR=<path>' to your shell config")
	}
	name := args[0]
	filePath := filepath.Join(m.eng.ResourceDir(), name)
	if _, err := os.Stat(filePath); err == nil {
		return errResult("/resource-new "+name, fmt.Sprintf("resource %q already exists — use /resource-edit to edit it", name))
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return errResult("/resource-new "+name, err.Error())
	}
	if err := os.WriteFile(filePath, nil, 0o644); err != nil {
		return errResult("/resource-new "+name, err.Error())
	}
	editorCmd := exec.Command(editor, filePath)
	return cmdResult{
		input:           "/resource-new " + name,
		suppressCmdPane: true,
		execCmd: tea.ExecProcess(editorCmd, func(err error) tea.Msg {
			return resourceReloadMsg{name: name}
		}),
	}
}

// =============================================================================
// profile commands
// =============================================================================

func cmdProfileInfo(m *Model, args []string) cmdResult {
	code := m.eng.ProfileCode()
	if len(args) > 0 {
		code = args[0]
	}
	p, ok := m.cfg.Profiles[code]
	if !ok {
		return errResult("/profile "+code, fmt.Sprintf("profile %q not found", code))
	}

	row := func(label, value string) string {
		return fmt.Sprintf("  %-12s%s", label+":", value)
	}

	var lines []string
	lines = append(lines, row("profile", code))
	lines = append(lines, row("provider", p.Provider))
	if p.Host != "" {
		lines = append(lines, row("host", p.Host))
	}
	lines = append(lines, row("model", p.Model))
	if p.MaxContextTokens > 0 {
		ctx := fmt.Sprintf("%d tokens", p.MaxContextTokens)
		if p.ContextTokenLimit > 0 {
			ctx += fmt.Sprintf("  (limit: %d)", p.ContextTokenLimit)
		}
		lines = append(lines, row("context", ctx))
	}
	if p.MaxUserMessages > 0 {
		lines = append(lines, row("messages", fmt.Sprintf("%d max", p.MaxUserMessages)))
	}
	if p.MaxOutputTokens > 0 {
		lines = append(lines, row("max output", fmt.Sprintf("%d tokens", p.MaxOutputTokens)))
	}
	if p.Strategy != "" {
		strat := p.Strategy
		if p.SummarizerProfile != "" {
			strat += fmt.Sprintf("  (via %s)", p.SummarizerProfile)
		}
		lines = append(lines, row("strategy", strat))
	}
	if inPer1M, outPer1M, ok := config.ExtractPricing(p.Info); ok {
		lines = append(lines, row("pricing", fmt.Sprintf("$%.2f / $%.2f per 1M tokens  (in / out)", inPer1M, outPer1M)))
	}

	return okResult("/profile "+code, lines)
}

func cmdProfileSwitch(m *Model, args []string) cmdResult {
	if len(args) == 0 {
		return errResult("/profile-switch", "usage: /profile-switch <name>")
	}
	name := args[0]
	if err := m.eng.SwitchProfile(name); err != nil {
		return errResult("/profile-switch "+name, err.Error())
	}
	m.cfg = m.eng.Config()
	return okResult("/profile-switch "+name, []string{fmt.Sprintf("switched to profile %q", name)})
}

func cmdProfileList(m *Model, args []string) cmdResult {
	if len(m.cfg.Profiles) == 0 {
		return okResult("/profile-list", []string{"(no profiles configured)"})
	}
	cur := m.eng.ProfileCode()

	// If specific names requested, validate them first.
	if len(args) > 0 {
		for _, name := range args {
			if _, ok := m.cfg.Profiles[name]; !ok {
				return errResult("/profile-list", fmt.Sprintf("profile %q not found", name))
			}
		}
	}

	// Build name list: args order when filtered, alphabetical otherwise.
	var names []string
	if len(args) > 0 {
		names = make([]string, len(args))
		for i, a := range args {
			names[len(args)-1-i] = a
		}
	} else {
		names = make([]string, 0, len(m.cfg.Profiles))
		for code := range m.cfg.Profiles {
			names = append(names, code)
		}
		sort.Strings(names)
	}
	sort.Strings(names)

	ctxStr := func(n int) string {
		if n == 0 {
			return ""
		}
		return ctxAbbrev(n)
	}

	type profileRow struct {
		name, provider, model, msgs, ctx, limit, strategy, summarizer, pricing string
		current                                                                 bool
	}

	rows := make([]profileRow, len(names))
	for i, code := range names {
		p := m.cfg.Profiles[code]
		r := profileRow{
			name:     code,
			provider: p.Provider,
			model:    p.Model,
			ctx:      ctxStr(p.MaxContextTokens),
			current:  code == cur,
		}
		if p.MaxUserMessages > 0 {
			r.msgs = fmt.Sprintf("%d", p.MaxUserMessages)
		}
		if p.ContextTokenLimit > 0 {
			r.limit = ctxStr(p.ContextTokenLimit)
		}
		if p.Strategy != "" {
			r.strategy = p.Strategy
		}
		if p.SummarizerProfile != "" {
			r.summarizer = p.SummarizerProfile
		}
		if inPer1M, outPer1M, ok := config.ExtractPricing(p.Info); ok {
			r.pricing = fmt.Sprintf("$%.2f/$%.2f", inPer1M, outPer1M)
		}
		rows[i] = r
	}

	// Compute column widths from header + data.
	headers := [9]string{"name", "provider", "model", "msgs", "context", "limit", "strategy", "summarizer", "pricing"}
	widths := [9]int{}
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		vals := [9]string{r.name, r.provider, r.model, r.msgs, r.ctx, r.limit, r.strategy, r.summarizer, r.pricing}
		for i, v := range vals {
			if len(v) > widths[i] {
				widths[i] = len(v)
			}
		}
	}

	fmtRow := func(vals [9]string) string {
		var sb strings.Builder
		sb.WriteString("  ")
		for i, v := range vals {
			if i == len(vals)-1 {
				sb.WriteString(v)
			} else {
				sb.WriteString(fmt.Sprintf("%-*s  ", widths[i], v))
			}
		}
		return strings.TrimRight(sb.String(), " ")
	}

	sepW := 0
	for i, w := range widths {
		sepW += w
		if i < len(widths)-1 {
			sepW += 2
		}
	}
	sep := "  " + strings.Repeat("─", sepW)

	lines := []string{fmtRow(headers), sep}
	for _, r := range rows {
		line := fmtRow([9]string{r.name, r.provider, r.model, r.msgs, r.ctx, r.limit, r.strategy, r.summarizer, r.pricing})
		if r.current {
			line += "  ←"
		}
		lines = append(lines, line)
	}
	res := okResult("/profile-list", lines)
	res.spokenText = "Profiles: " + strings.Join(names, ", ") + ". Current: " + cur + "."
	return res
}

func cmdProfileDefault(m *Model) cmdResult {
	def := m.cfg.DefaultProfile
	if def == "" {
		def = "(not set)"
	}
	return okResult("/profile-default", []string{"default profile: " + def})
}

func cmdProfileDefaultSet(m *Model, args []string) cmdResult {
	if len(args) == 0 {
		return errResult("/profile-default-set", "usage: /profile-default-set <name>")
	}
	name := args[0]
	if err := m.eng.SetDefaultProfile(name); err != nil {
		return errResult("/profile-default-set "+name, err.Error())
	}
	m.cfg = m.eng.Config()
	return okResult("/profile-default-set "+name, []string{fmt.Sprintf("default profile set to %q", name)})
}

// =============================================================================
// system commands
// =============================================================================

func cmdSystem(m *Model, args []string) cmdResult {
	text := m.eng.SystemPrompt()
	if text == "" {
		return okResult("/system", []string{"(no system prompt)"})
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	return okResult("/system", lines)
}

func cmdSystemSet(m *Model, args []string) cmdResult {
	if len(args) == 0 {
		return errResult("/system-set", "usage: /system-set <text>")
	}
	text := strings.Join(args, " ")
	if err := m.eng.SetSystem(text); err != nil {
		return errResult("/system-set", err.Error())
	}
	return okResult("/system-set", []string{"system prompt updated"})
}

func cmdSystemClear(m *Model) cmdResult {
	if err := m.eng.SetSystem(""); err != nil {
		return errResult("/system-clear", err.Error())
	}
	return okResult("/system-clear", []string{"system prompt cleared"})
}

// =============================================================================
// info commands
// =============================================================================

func cmdConfig(m *Model) cmdResult {
	cfg := m.eng.Config()

	row := func(label, value string) string {
		return fmt.Sprintf("  %-17s%s", label+":", value)
	}

	lines := []string{
		row("config file", config.DefaultConfigPath()),
		row("topics root", cfg.TopicsRoot),
		row("default topic", orNone(cfg.DefaultTopic)),
		row("default profile", orNone(cfg.DefaultProfile)),
		row("window messages", fmt.Sprintf("%d", cfg.WindowMessages)),
	}

	if len(cfg.Profiles) > 0 {
		lines = append(lines, fmt.Sprintf("  profiles (%d):", len(cfg.Profiles)))

		// Sort alphabetically.
		names := make([]string, 0, len(cfg.Profiles))
		for code := range cfg.Profiles {
			names = append(names, code)
		}
		sort.Strings(names)

		for _, code := range names {
			p := cfg.Profiles[code]
			parts := []string{p.Provider, p.Model}
			if p.Strategy != "" {
				parts = append(parts, p.Strategy)
			}
			if p.MaxContextTokens > 0 {
				parts = append(parts, fmt.Sprintf("ctx:%s", ctxAbbrev(p.MaxContextTokens)))
			}
			if inPer1M, outPer1M, ok := config.ExtractPricing(p.Info); ok {
				parts = append(parts, fmt.Sprintf("$%.2f/$%.2f", inPer1M, outPer1M))
			}
			marker := ""
			if code == cfg.DefaultProfile {
				marker = " ←"
			}
			lines = append(lines, fmt.Sprintf("    %-16s%s%s", code, strings.Join(parts, ", "), marker))
		}
	}

	return okResult("/config", lines)
}

func ctxAbbrev(n int) string {
	if n >= 2000000 {
		if n%1000000 == 0 {
			return fmt.Sprintf("%dM", n/1000000)
		}
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 2000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

func cmdStatus(m *Model) cmdResult {
	p := m.eng.Profile()
	lines := []string{
		fmt.Sprintf("topic:    %s", m.eng.TopicName()),
		fmt.Sprintf("profile:  %s (%s/%s)", m.eng.ProfileCode(), p.Provider, p.Model),
		fmt.Sprintf("c2 home:  %s", m.eng.DataDir()),
		fmt.Sprintf("terminal: %s", TerminalName()),
		fmt.Sprintf("theme:    %s", m.themeMode),
	}
	return okResult("/status", lines)
}

func cmdStats(m *Model) cmdResult {
	logPath := store.UsageLogPath(m.eng.DataDir())
	entries, err := store.ReadUsageLog(logPath)
	if err != nil || len(entries) == 0 {
		return okResult("/stats", []string{"(no usage recorded)"})
	}
	agg := store.AggregateUsage(entries, m.eng.TopicName(), 0)
	aggAll := store.AggregateUsage(entries, "", 0)
	lines := []string{
		fmt.Sprintf("topic %q:", m.eng.TopicName()),
		fmt.Sprintf("  calls:  %d", agg.Total.Calls),
		fmt.Sprintf("  tokens: %s in / %s out",
			core.FormatTokens(agg.Total.InputTokens),
			core.FormatTokens(agg.Total.OutputTokens)),
		fmt.Sprintf("  cost:   %s", config.FormatCost(agg.Total.CostUSD)),
		"all topics:",
		fmt.Sprintf("  calls:  %d", aggAll.Total.Calls),
		fmt.Sprintf("  tokens: %s in / %s out",
			core.FormatTokens(aggAll.Total.InputTokens),
			core.FormatTokens(aggAll.Total.OutputTokens)),
		fmt.Sprintf("  cost:   %s", config.FormatCost(aggAll.Total.CostUSD)),
	}
	return okResult("/stats", lines)
}

// =============================================================================
// delete-last
// =============================================================================

func cmdDeleteLast(m *Model, args []string) cmdResult {
	n := 1
	if len(args) > 0 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v < 1 {
			return errResult("/delete-last", "usage: /delete-last [n]  (n must be a positive integer)")
		}
		n = v
	}

	// Single entry: execute immediately unless --ack-all-deletions is set.
	if n == 1 && !m.ackAllDeletions {
		if len(m.exchanges) == 0 {
			return okResult("/delete-last", []string{"nothing to delete"})
		}
		removed, err := m.eng.DeleteLast(1)
		if err != nil {
			return errResult("/delete-last", err.Error())
		}
		m.exchanges = nil
		m.loadHistory()
		return okResult("/delete-last", []string{fmt.Sprintf("deleted %d exchange(s)", removed)})
	}

	// Confirmation flow (multiple entries, or n==1 with --ack-all-deletions).
	if len(m.exchanges) == 0 {
		return okResult("/delete-last", []string{"nothing to delete"})
	}
	noun := fmt.Sprintf("last %d exchange(s)", n)
	if n == 1 {
		noun = "last exchange"
	}
	m.pendingAction = func() cmdResult {
		removed, err := m.eng.DeleteLast(n)
		if err != nil {
			return errResult("/delete-last", err.Error())
		}
		return okResult("/delete-last", []string{fmt.Sprintf("deleted %d exchange(s)", removed)})
	}
	m.pendingPost = func(cur *Model) {
		cur.exchanges = nil
		cur.loadHistory()
	}
	return okResult("/delete-last", []string{
		fmt.Sprintf("The %s will be permanently deleted.", noun),
		"[yes] to confirm, other input or Esc to cancel:",
	})
}

func cmdTTS(m *Model, args []string) cmdResult {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "on":
		m.ttsAuto = true
		return okResult("/tts on", []string{"TTS auto-mode enabled — each response will be spoken automatically"})
	case "off":
		m.ttsAuto = false
		// Stop any current playback.
		if m.isTTSPlaying() {
			m.killTTS()
			m.rebuildConvContent()
		}
		return okResult("/tts off", []string{"TTS auto-mode disabled"})
	case "":
		// Toggle.
		if m.ttsAuto {
			return cmdTTS(m, []string{"off"})
		}
		return cmdTTS(m, []string{"on"})
	default:
		state := "off"
		if m.ttsAuto {
			state = "on"
		}
		return errResult("/tts "+sub, fmt.Sprintf("usage: /tts [on|off]  (currently %s)", state))
	}
}

func cmdFold(m *Model) cmdResult {
	longCount := 0
	for i := range m.exchanges {
		if m.isLongEntry(m.exchanges[i]) {
			m.exchanges[i].expanded = false
			longCount++
		}
	}
	m.rebuildConvContent()
	if longCount == 0 {
		return okResult("/fold", []string{"no long entries to fold"})
	}
	return okResult("/fold", []string{fmt.Sprintf("folded %d long entr%s", longCount, map[bool]string{true: "y", false: "ies"}[longCount == 1])})
}

func cmdUnfold(m *Model) cmdResult {
	longCount := 0
	for i := range m.exchanges {
		if m.isLongEntry(m.exchanges[i]) {
			m.exchanges[i].expanded = true
			longCount++
		}
	}
	m.rebuildConvContent()
	if longCount == 0 {
		return okResult("/unfold", []string{"no long entries to unfold"})
	}
	return okResult("/unfold", []string{fmt.Sprintf("unfolded %d long entr%s", longCount, map[bool]string{true: "y", false: "ies"}[longCount == 1])})
}

func cmdPlayAll(m *Model) cmdResult {
	if m.isTTSPlaying() {
		// Already playing — stop everything.
		m.killTTS()
		m.rebuildConvContent()
		return okResult("/play-all", []string{"playback stopped"})
	}
	if len(m.exchanges) == 0 {
		return okResult("/play-all", []string{"no entries to play"})
	}
	// Queue all indices — Update will drain them after the command pane is shown.
	queue := make([]int, len(m.exchanges))
	for i := range m.exchanges {
		queue[i] = i
	}
	m.ttsQueue = queue
	return okResult("/play-all", []string{fmt.Sprintf("playing %d entries — s or Ctrl+C to stop", len(m.exchanges))})
}

// =============================================================================
// completion
// =============================================================================

// completionEntry is one candidate in the command completion list.
type completionEntry struct {
	cmd  string // e.g. "/topic-list"
	desc string // e.g. "list all topics"
}

// allCompletions returns the full command catalogue in display order.
func allCompletions() []completionEntry {
	return []completionEntry{
		{"/topic", "show topic info"},
		{"/topic-switch", "switch to existing topic"},
		{"/topic-new", "create and switch to new topic"},
		{"/topic-list", "list all topics"},
		{"/topic-delete", "delete a topic"},
		{"/topic-clear", "clear history for current topic"},
		{"/topic-default", "show default topic"},
		{"/topic-default-set", "set default topic"},
		{"/topic-summary", "show current context summary"},
		{"/topic-history", "show last N exchanges"},
		{"/resource-add", "add resource file to current topic"},
		{"/resource-new", "create a new resource file and open in $EDITOR"},
		{"/resource-list", "list resources for topic"},
		{"/resource-remove", "remove a resource from topic"},
		{"/resource-delete", "delete a resource from topic (alias for remove)"},
		{"/resource-view", "open a resource file in the viewer"},
		{"/resource-edit", "edit a resource file in $EDITOR"},
		{"/profile", "show profile info"},
		{"/profile-switch", "switch to named profile"},
		{"/profile-list", "list all profiles"},
		{"/profile-default", "show default profile"},
		{"/profile-default-set", "set default profile"},
		{"/system", "show system prompt"},
		{"/system-set", "set system prompt"},
		{"/system-clear", "remove system prompt"},
		{"/config", "show resolved configuration"},
		{"/status", "show effective defaults"},
		{"/stats", "show usage and cost stats"},
		{"/voice-commands", "list all voice commands and their spoken phrases"},
		{"/log", "open/close debug log tail in a new terminal window"},
		{"/export", "export conversation to file"},
		{"/help [group]", "show all commands or commands for a group (topic|resource|profile|system|info|notes|nav|theme|keys|files)"},
		{"/delete-last", "delete last N exchanges from history"},
		{"/tts [on|off]", "toggle TTS auto-mode"},
		{"/fold", "collapse all long entries"},
		{"/unfold", "expand all long entries"},
		{"/play-all", "play all entries via TTS (toggle)"},
		{"/block-keys", "show keys available when a block is focused"},
		{"/theme", "switch or show theme: light | dark | auto | options"},
		{"/exit", "exit c2"},
	}
}

// contextualParams returns candidate parameter values for the given command,
// or nil if no contextual completion is available for that command.
func contextualParams(m *Model, cmd string) []string {
	switch cmd {
	case "/profile-switch", "/profile-default-set":
		names := make([]string, 0, len(m.cfg.Profiles))
		for name := range m.cfg.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		return names

	case "/topic-switch", "/topic-delete", "/topic-default-set":
		topics, err := m.eng.ListTopics()
		if err != nil {
			return nil
		}
		return topics

	case "/resource-remove", "/resource-delete", "/resource-view", "/resource-edit":
		st := store.New(m.cfg.TopicsRoot)
		files, err := st.ListResources(m.eng.TopicName())
		if err != nil {
			return nil
		}
		names := make([]string, len(files))
		for i, fi := range files {
			names[i] = fi.Name()
		}
		return names

	case "/export":
		defaultName := fmt.Sprintf("export-%s-%s.md", m.eng.TopicName(), time.Now().Format("2006-01-02"))
		return []string{defaultName}

	case "/help":
		return []string{"topic", "resource", "profile", "system", "info", "notes", "nav", "theme", "keys", "files", "all"}
	}
	return nil
}

// filterCompletions returns entries whose command starts with the given prefix.
func filterCompletions(prefix string) []completionEntry {
	var out []completionEntry
	for _, e := range allCompletions() {
		if strings.HasPrefix(e.cmd, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// =============================================================================
// log
// =============================================================================

func cmdLog(m *Model) cmdResult {
	pid := os.Getpid()
	sentinelPath := fmt.Sprintf("%s/c2-log-stop-%d", os.TempDir(), pid)

	// Toggle: if already open, write sentinel to signal the script to exit.
	if m.logViewerOpen {
		_ = os.WriteFile(sentinelPath, nil, 0o644)
		m.logViewerOpen = false
		return cmdResult{
			input:  "/log",
			output: []string{"debug log viewer closed"},
		}
	}

	logPath := os.ExpandEnv("$HOME/.c2/debug.log")
	scriptPath := fmt.Sprintf("%s/c2-log-viewer-%d.sh", os.TempDir(), pid)

	// Script: tail the log, exit when c2 exits OR sentinel file appears; self-deletes on exit.
	script := fmt.Sprintf(
		"#!/bin/bash\ntrap 'rm -f \"%s\" \"%s\"' EXIT\ntail -n 200 -f '%s' & __t=$!\nwhile kill -0 %d 2>/dev/null && [ ! -f '%s' ]; do sleep 1; done\nkill $__t 2>/dev/null\n",
		scriptPath, sentinelPath, logPath, pid, sentinelPath,
	)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return errResult("/log", fmt.Sprintf("could not write log script: %v", err))
	}

	var appleScript string
	switch ActiveTerminal {
	case TermITerm2:
		appleScript = fmt.Sprintf(
			`tell application "iTerm2" to create window with default profile command "%s"`,
			scriptPath,
		)
	default:
		appleScript = fmt.Sprintf(
			`tell application "Terminal" to do script "%s"`,
			scriptPath,
		)
	}
	go exec.Command("osascript", "-e", appleScript).Run()
	m.logViewerOpen = true

	return cmdResult{
		input:  "/log",
		output: []string{"debug log opened in new window — /log again to close"},
	}
}

// =============================================================================
// export
// =============================================================================

func cmdExport(m *Model, args []string) cmdResult {
	// Resolve destination path.
	var destPath string
	defaultName := fmt.Sprintf("export-%s-%s.md", m.eng.TopicName(), time.Now().Format("2006-01-02"))
	if len(args) == 0 {
		destPath = filepath.Join(m.eng.ResourceDir(), defaultName)
	} else {
		name := args[0]
		// If no directory component, save to resources.
		if !strings.HasPrefix(name, "/") && !strings.HasPrefix(name, "~/") && !strings.HasPrefix(name, "./") && !strings.HasPrefix(name, "../") && !strings.Contains(name, string(os.PathSeparator)) {
			destPath = filepath.Join(m.eng.ResourceDir(), name)
		} else {
			if strings.HasPrefix(name, "~/") {
				home, _ := os.UserHomeDir()
				name = filepath.Join(home, name[2:])
			}
			destPath = name
		}
	}

	// Determine format from extension.
	markdown := strings.ToLower(filepath.Ext(destPath)) == ".md"

	// Build content.
	var sb strings.Builder
	topic := m.eng.TopicName()
	profileCode := m.eng.ProfileCode()
	profileModel := m.eng.Profile().Model
	now := time.Now().Format("2006-01-02 15:04")

	if markdown {
		fmt.Fprintf(&sb, "# %s\n", topic)
		fmt.Fprintf(&sb, "Date: %s  \n", now)
		fmt.Fprintf(&sb, "Profile: %s / %s\n", profileCode, profileModel)
	} else {
		fmt.Fprintf(&sb, "Export: %s\n", topic)
		fmt.Fprintf(&sb, "Date: %s\n", now)
		fmt.Fprintf(&sb, "Profile: %s / %s\n", profileCode, profileModel)
	}

	sep := "\n---\n"
	if !markdown {
		sep = "\n===\n"
	}

	for _, ex := range m.exchanges {
		sb.WriteString(sep)
		if ex.isNote {
			if markdown {
				fmt.Fprintf(&sb, "\n📌 %s\n", ex.userMsg.Content)
			} else {
				fmt.Fprintf(&sb, "\n[note] %s\n", ex.userMsg.Content)
			}
			continue
		}
		if markdown {
			fmt.Fprintf(&sb, "\n**you:** %s\n", ex.userMsg.Content)
			if ex.asstMsg.Content != "" {
				fmt.Fprintf(&sb, "\n**%s:** %s\n", profileModel, ex.asstMsg.Content)
			}
		} else {
			fmt.Fprintf(&sb, "\nyou: %s\n", ex.userMsg.Content)
			if ex.asstMsg.Content != "" {
				fmt.Fprintf(&sb, "\n%s: %s\n", profileModel, ex.asstMsg.Content)
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return errResult("/export", err.Error())
	}
	if err := os.WriteFile(destPath, []byte(sb.String()), 0644); err != nil {
		return errResult("/export", err.Error())
	}

	return okResult("/export", []string{fmt.Sprintf("saved to %s", destPath)})
}

// =============================================================================
// help
// =============================================================================

func cmdVoiceCommands() cmdResult {
	type entry struct {
		label    string
		synonyms string
	}
	groups := []struct {
		name    string
		labels  []string
	}{
		{"session", []string{"clear", "session_start", "session_end", "session_resume", "stop", "resume"}},
		{"conversation", []string{"ask_llm", "chat_note", "chat_replay", "chat_play_last", "chat_play_all", "play_all", "clear_input", "delete_last", "fold", "unfold", "tts_toggle"}},
		{"topic", []string{"topic_info", "topic_list", "topic_switch", "topic_new", "topic_clear", "topic_delete", "topic_default", "topic_default_set", "topic_summary", "topic_history"}},
		{"profile", []string{"profile_info", "profile_list", "profile_switch", "profile_default", "profile_default_set"}},
		{"resource", []string{"resource_list", "resource_view", "resource_edit", "resource_remove"}},
		{"system", []string{"system_show", "system_clear"}},
		{"info", []string{"status", "voice_status", "stats", "config", "voice_commands", "help"}},
	}

	var lines []string
	for _, g := range groups {
		lines = append(lines, g.name+":")
		for _, label := range g.labels {
			phrases := commandSynonyms[label]
			if len(phrases) == 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf("  %-20s  %s", label, strings.Join(phrases, ", ")))
		}
		lines = append(lines, "")
	}
	// trim trailing blank
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return okResult("/voice-commands", lines)
}

func cmdHelp(cmd string, args []string) cmdResult {
	type entry struct {
		cmd  string
		desc string
	}
	groups := map[string][]entry{
		"topic": {
			{"/topic [name]", "show topic info"},
			{"/topic-switch <name>", "switch to existing topic"},
			{"/topic-new <name>", "create and switch to new topic"},
			{"/topic-list", "list all topics"},
			{"/topic-delete", "delete current topic"},
			{"/topic-clear", "clear history for current topic"},
			{"/topic-default", "show default topic"},
			{"/topic-default-set <name>", "set default topic"},
			{"/topic-summary", "show current context summary"},
			{"/topic-history [n]", "show last N exchanges"},
		},
		"resource": {
			{"/resource-list [topic]", "list resources for topic"},
			{"/resource-add <file>", "copy file into topic resources"},
			{"/resource-new <name>", "create a new resource file and open in $EDITOR"},
			{"/resource-remove <name>", "delete a resource from topic"},
			{"/resource-delete <name>", "delete a resource from topic (alias for remove)"},
			{"/resource-view <name>", "open a resource file in the viewer"},
			{"/resource-edit <name>", "edit a resource file in $EDITOR"},
		},
		"profile": {
			{"/profile", "show current profile"},
			{"/profile-switch <name>", "switch to named profile"},
			{"/profile-list", "list all profiles"},
			{"/profile-default", "show default profile"},
			{"/profile-default-set <name>", "set default profile"},
		},
		"system": {
			{"/system", "show system prompt"},
			{"/system-set <text>", "set system prompt"},
			{"/system-clear", "remove system prompt"},
		},
		"info": {
			{"/config", "show resolved configuration"},
			{"/status", "show effective defaults"},
			{"/stats", "show usage and cost stats"},
			{"/voice-commands", "list all voice commands and their spoken phrases"},
			{"/export [file]", "export conversation; default saves to topic resources as export-topic-date.md"},
			{"/help [group]", "show all commands or commands for a group"},
			{"/delete-last [n]", "delete last N exchanges from history (default 1)"},
			{"/exit", "exit c2"},
		},
		"notes": {
			{"// <text>", "save a personal note (not sent to LLM)"},
		},
		"view": {
			{"/tts [on|off]", "auto-speak each response (toggle; no arg = toggle)"},
			{"/fold", "collapse all long entries"},
			{"/unfold", "expand all long entries"},
			{"/play-all", "play all entries via TTS — s or Ctrl+C to stop"},
		},
		"nav": {
			{"/block-keys", "show keys available when a block is focused"},
		},
		"theme": {
			{"/theme [light|dark|auto|options]", "switch theme or show current"},
		},
	}

	filesSection := []string{
		"files:",
		"  Append one or more @ref tokens to any prompt to inject file content.",
		"  The surrounding text becomes the instruction; each file becomes a block.",
		"  Multiple refs are resolved left-to-right; all must exist or the send aborts.",
		"",
		"  @name               topic resources folder  (resources/name)",
		"  @subdir/name        topic resources folder  (resources/subdir/name)",
		"  @./path  @../path   relative filesystem path (from current directory)",
		"  @/absolute/path     absolute filesystem path",
		"  @~/path             home-relative filesystem path",
		"",
		"  Examples:",
		"    explain this @main.go",
		"    compare @old.py and @new.py and list the differences",
		"    what does this do @./scripts/run.sh",
		"    summarize @notes.txt and cross-check with @~/docs/spec.md",
	}

	order := []string{"topic", "resource", "profile", "system", "info", "notes", "nav", "theme", "keys", "files"}

	noun := ""
	if len(args) > 0 {
		noun = strings.ToLower(args[0])
	}

	keysSection := []string{
		"keys:",
		"  global:",
		fmt.Sprintf("  %-14s  %s", "Enter", "send prompt"),
		fmt.Sprintf("  %-14s  %s", "Ctrl+J", "insert newline"),
		fmt.Sprintf("  %-14s  %s", "Ctrl+G", "correct spelling/grammar (replaced in-place)"),
		fmt.Sprintf("  %-14s  %s", "Tab", "fill completion / toggle voice↔text mode"),
		fmt.Sprintf("  %-14s  %s", "Esc", "close pane / clear input / kill TTS / back to input"),
		fmt.Sprintf("  %-14s  %s", "Ctrl+C", "cancel streaming / quit"),
		fmt.Sprintf("  %-14s  %s", "Ctrl+L", "clear screen"),
		fmt.Sprintf("  %-14s  %s", "Ctrl+T", "switch topic (picker overlay)"),
		fmt.Sprintf("  %-14s  %s", "Ctrl+P", "switch profile (picker overlay)"),
		fmt.Sprintf("  %-14s  %s", "Ctrl+N", "toggle focus: input ↔ conversation"),
		fmt.Sprintf("  %-14s  %s", "Ctrl+S", "copy focused exchange or input to clipboard"),
		fmt.Sprintf("  %-14s  %s", "Ctrl+X", "close any overlay"),
		fmt.Sprintf("  %-14s  %s", "Ctrl+Space", "activate/cancel voice command (voice mode)"),
		"",
		"  conversation pane (Tab to enter, ↑↓ to navigate):",
		fmt.Sprintf("  %-14s  %s", "↑ / ↓", "navigate exchanges"),
		fmt.Sprintf("  %-14s  %s", "PgUp / PgDn", "scroll content"),
		fmt.Sprintf("  %-14s  %s", "v", "fold/unfold current entry"),
		fmt.Sprintf("  %-14s  %s", "s", "speak current entry (TTS)"),
		fmt.Sprintf("  %-14s  %s", "x", "delete current entry (with confirmation)"),
		"",
		"  TTS playback:",
		fmt.Sprintf("  %-14s  %s", "[  /  ]", "decrease / increase speed"),
		fmt.Sprintf("  %-14s  %s", "s", "stop playback"),
		"",
		"  resource viewer (Ctrl+X or q to close):",
		fmt.Sprintf("  %-14s  %s", "↑ / ↓", "move cursor line by line"),
		fmt.Sprintf("  %-14s  %s", "PgUp / PgDn", "move cursor by page"),
		fmt.Sprintf("  %-14s  %s", "g / G", "jump to top / bottom"),
		fmt.Sprintf("  %-14s  %s", "s", "start/stop TTS from cursor (line-by-line)"),
		fmt.Sprintf("  %-14s  %s", "e", "open in $EDITOR"),
		fmt.Sprintf("  %-14s  %s", "[  /  ]", "decrease / increase TTS speed"),
	}

	renderGroup := func(g string) []string {
		if g == "files" {
			return filesSection
		}
		if g == "keys" {
			return keysSection
		}
		entries := groups[g]
		out := []string{g + ":"}
		for _, e := range entries {
			out = append(out, fmt.Sprintf("  %-32s  %s", e.cmd, e.desc))
		}
		return out
	}

	var lines []string
	if noun == "" || noun == "all" {
		for _, g := range order {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, renderGroup(g)...)
		}
	} else if noun == "files" || noun == "nav" || noun == "theme" || noun == "keys" || groups[noun] != nil {
		lines = renderGroup(noun)
	} else {
		return errResult(cmd+" "+noun, fmt.Sprintf("unknown group %q — available: %s", noun, strings.Join(order, "|")))
	}
	return okResult(cmd, lines)
}

// =============================================================================
// theme command
// =============================================================================

func cmdTheme(m *Model, args []string) cmdResult {
	if len(args) == 0 {
		return okResult("/theme", []string{
			fmt.Sprintf("current theme: %s", m.themeMode),
			"",
			"usage: /theme [light|dark|auto|options]",
		})
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "light":
		m.themeMode = "light"
		ActiveTheme = Light
		AdjustThemeForTerminal()
		m.rebuildConvContent()
		return okResult("/theme light", []string{"theme set to light"})
	case "dark":
		m.themeMode = "dark"
		ActiveTheme = Nord
		AdjustThemeForTerminal()
		m.rebuildConvContent()
		return okResult("/theme dark", []string{"theme set to dark (Nord)"})
	case "auto":
		m.themeMode = "auto"
		DetectTheme()
		AdjustThemeForTerminal()
		m.rebuildConvContent()
		return okResult("/theme auto", []string{fmt.Sprintf("theme set to auto (detected: %s)", detectedThemeName())})
	case "options":
		return okResult("/theme options", []string{
			"available themes:",
			"  light   — optimised for light-background iTerm2 profiles",
			"  dark    — Nord palette (default dark theme)",
			"  auto    — detect from terminal COLORFGBG (default)",
		})
	default:
		return errResult("/theme "+sub, "unknown theme — use: light | dark | auto | options")
	}
}

// detectedThemeName returns the name of the theme DetectTheme() would select.
func detectedThemeName() string {
	if ActiveTerminal != TermITerm2 {
		return "dark"
	}
	fgbg := os.Getenv("COLORFGBG")
	if fgbg == "" {
		return "dark"
	}
	parts := strings.SplitN(fgbg, ";", 2)
	if len(parts) != 2 {
		return "dark"
	}
	var bg int
	fmt.Sscanf(parts[1], "%d", &bg)
	if bg >= 8 {
		return "light"
	}
	return "dark"
}

// =============================================================================
// nav commands
// =============================================================================

func cmdBlockKeys() cmdResult {
	return okResult("/block-keys", []string{
		"Block navigation keys (enter nav mode with Ctrl+N, then ↑/↓ to select a block):",
		"",
		fmt.Sprintf("  %-10s  %s", "v", "expand / collapse long content"),
		fmt.Sprintf("  %-10s  %s", "s", "speak block via TTS (toggle)"),
		fmt.Sprintf("  %-10s  %s", "x", "delete block (with confirmation)"),
		fmt.Sprintf("  %-10s  %s", "Ctrl+N", "return to input pane"),
		fmt.Sprintf("  %-10s  %s", "Esc", "return to input pane"),
	})
}

// =============================================================================
// helpers
// =============================================================================

func okResult(input string, output []string) cmdResult {
	return cmdResult{input: input, output: output, isError: false}
}

func errResult(input, msg string) cmdResult {
	return cmdResult{input: input, output: []string{msg}, isError: true}
}

func yesNoStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func totalTokens(h *core.History) int {
	total := 0
	for _, m := range h.Msgs {
		total += core.ApproxTokens(m.Content)
	}
	return total
}
