package store

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jrniemiec/c2/core"
)

// FileStore stores topics on the local filesystem.
//
//	<Root>/<topic>/system.txt
//	<Root>/<topic>/history.json
//	<Root>/<topic>/summary.txt
//	<Root>/<topic>/resources/
type FileStore struct {
	Root string
}

func New(root string) *FileStore {
	return &FileStore{Root: root}
}

// sanitize ensures topic is a safe relative path under Root.
func sanitize(topicName string) (string, error) {
	name := strings.TrimSpace(topicName)
	if name == "" {
		return "", fmt.Errorf("topic name is empty")
	}
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("topic must be a relative path: %q", topicName)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("topic cannot escape topics root: %q", topicName)
	}
	for _, p := range strings.Split(clean, string(filepath.Separator)) {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("invalid topic path: %q", topicName)
		}
	}
	return clean, nil
}

func (s *FileStore) topicDir(name string) (string, error) {
	rel, err := sanitize(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Root, rel), nil
}

func (s *FileStore) ensureTopicDir(name string) (string, error) {
	dir, err := s.topicDir(name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *FileStore) historyPath(name string) (string, error) {
	dir, err := s.topicDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "history.json"), nil
}

func (s *FileStore) systemPath(name string) (string, error) {
	dir, err := s.topicDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "system.txt"), nil
}

func (s *FileStore) summaryPath(name string) (string, error) {
	dir, err := s.topicDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "summary.txt"), nil
}

// ListTopics returns topic names (relative paths) that contain history.json or system.txt.
func (s *FileStore) ListTopics() ([]string, error) {
	if _, err := os.Stat(s.Root); err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	var topics []string
	err := filepath.WalkDir(s.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if filepath.Clean(path) == filepath.Clean(s.Root) {
			return nil
		}
		if fileExists(filepath.Join(path, "history.json")) || fileExists(filepath.Join(path, "system.txt")) {
			rel, err := filepath.Rel(s.Root, path)
			if err != nil {
				return err
			}
			topics = append(topics, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(topics)
	return topics, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// LoadTopic ensures the topic dir exists and loads system + history.
// A new topic always gets an empty history.json written so it appears in ListTopics.
func (s *FileStore) LoadTopic(name string) (*core.Topic, error) {
	if _, err := s.ensureTopicDir(name); err != nil {
		return nil, err
	}
	system, err := s.LoadSystem(name)
	if err != nil {
		return nil, err
	}
	history, err := s.LoadHistory(name)
	if err != nil {
		return nil, err
	}
	// Ensure history.json exists on disk so the topic is visible to ListTopics.
	hp, err := s.historyPath(name)
	if err != nil {
		return nil, err
	}
	if !fileExists(hp) {
		if err := saveHistoryFile(hp, history); err != nil {
			return nil, err
		}
	}
	return &core.Topic{
		Name:         name,
		SystemPrompt: system,
		History:      history,
	}, nil
}

// SaveTopic saves both system prompt and history.
func (s *FileStore) SaveTopic(t *core.Topic) error {
	if err := s.SaveSystem(t.Name, t.SystemPrompt); err != nil {
		return err
	}
	return s.SaveHistory(t.Name, t.History)
}

// LoadSystem reads system.txt (returns "" if missing).
func (s *FileStore) LoadSystem(name string) (string, error) {
	p, err := s.systemPath(name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

// SaveSystem writes system.txt.
func (s *FileStore) SaveSystem(name, system string) error {
	if _, err := s.ensureTopicDir(name); err != nil {
		return err
	}
	p, err := s.systemPath(name)
	if err != nil {
		return err
	}
	return os.WriteFile(p, []byte(system), 0644)
}

// LoadHistory loads history.json (returns empty history if missing).
func (s *FileStore) LoadHistory(name string) (*core.History, error) {
	if _, err := s.ensureTopicDir(name); err != nil {
		return nil, err
	}
	p, err := s.historyPath(name)
	if err != nil {
		return nil, err
	}
	return loadHistoryFile(p)
}

// SaveHistory writes history.json atomically.
func (s *FileStore) SaveHistory(name string, h *core.History) error {
	if _, err := s.ensureTopicDir(name); err != nil {
		return err
	}
	p, err := s.historyPath(name)
	if err != nil {
		return err
	}
	return saveHistoryFile(p, h)
}

// ClearHistory resets history and removes any stale summary.
func (s *FileStore) ClearHistory(name string) error {
	if _, err := s.ensureTopicDir(name); err != nil {
		return err
	}
	if sp, err := s.summaryPath(name); err == nil {
		_ = os.Remove(sp)
	}
	return s.SaveHistory(name, core.NewHistory())
}

// DeleteTopic removes the entire topic directory.
func (s *FileStore) DeleteTopic(name string) error {
	dir, err := s.topicDir(name)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// LoadSummary loads the cached summary for a topic.
// Returns ("", -1, nil) if no summary exists.
func (s *FileStore) LoadSummary(topicName string) (string, int, error) {
	p, err := s.summaryPath(topicName)
	if err != nil {
		return "", -1, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", -1, nil
		}
		return "", -1, err
	}
	return parseSummaryFile(string(b))
}

// SaveSummary persists the summary atomically.
func (s *FileStore) SaveSummary(topicName string, text string, coversThrough int) error {
	if _, err := s.ensureTopicDir(topicName); err != nil {
		return err
	}
	p, err := s.summaryPath(topicName)
	if err != nil {
		return err
	}
	content := fmt.Sprintf("covers_through: %d\n---\n%s", coversThrough, text)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// parseSummaryFile parses:
//
//	covers_through: 42
//	---
//	<summary text>
func parseSummaryFile(data string) (text string, coversThrough int, err error) {
	const sep = "\n---\n"
	idx := strings.Index(data, sep)
	if idx < 0 {
		return "", 0, fmt.Errorf("summary file: missing '---' separator")
	}
	header := strings.TrimSpace(data[:idx])
	body := data[idx+len(sep):]

	const prefix = "covers_through: "
	if !strings.HasPrefix(header, prefix) {
		return "", 0, fmt.Errorf("summary file: missing 'covers_through:' header")
	}
	n, err := strconv.Atoi(strings.TrimSpace(header[len(prefix):]))
	if err != nil {
		return "", 0, fmt.Errorf("summary file: bad covers_through value: %w", err)
	}
	return body, n, nil
}

// ListResources returns file info for all files in the topic's resources/ directory.
func (s *FileStore) ListResources(topicName string) ([]fs.FileInfo, error) {
	dir, err := s.topicDir(topicName)
	if err != nil {
		return nil, err
	}
	resDir := filepath.Join(dir, "resources")
	entries, err := os.ReadDir(resDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []fs.FileInfo{}, nil
		}
		return nil, err
	}
	var result []fs.FileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, info)
	}
	return result, nil
}

// DeleteResource removes a resource file by name from the topic's resources/ directory.
func (s *FileStore) DeleteResource(topicName, name string) error {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("invalid resource name: %q", name)
	}
	dir, err := s.topicDir(topicName)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "resources", filepath.Base(name))
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("resource not found: %s", name)
		}
		return err
	}
	return os.Remove(path)
}

// CreateResource copies a file into the topic's resources/ directory.
func (s *FileStore) CreateResource(topicName, sourcePath string) error {
	if sourcePath == "" {
		return fmt.Errorf("resource path is empty")
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read resource %q: %w", sourcePath, err)
	}
	base := filepath.Base(sourcePath)
	resDir := filepath.Join(func() string { d, _ := s.topicDir(topicName); return d }(), "resources")
	if err := os.MkdirAll(resDir, 0755); err != nil {
		return err
	}
	dst := filepath.Join(resDir, base)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("resource already exists: %s", base)
	}
	return os.WriteFile(dst, data, 0644)
}

// --- history file helpers ---

func loadHistoryFile(path string) (*core.History, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return core.NewHistory(), nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return core.NewHistory(), nil
	}
	var h core.History
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, err
	}
	if h.Msgs == nil {
		h.Msgs = []core.Message{}
	}
	return &h, nil
}

func saveHistoryFile(path string, h *core.History) error {
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
