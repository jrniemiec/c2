package store

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// UsageEntry is one record per LLM call appended to the JSONL usage log.
type UsageEntry struct {
	Timestamp    time.Time `json:"ts"`
	Topic        string    `json:"topic"`
	Profile      string    `json:"profile"`
	Model        string    `json:"model"`
	Provider     string    `json:"provider"`
	InputTokens  int       `json:"in"`
	OutputTokens int       `json:"out"`
	CostUSD      float64   `json:"cost_usd,omitempty"`
	Estimated    bool      `json:"estimated,omitempty"`
}

// UsageLogPath returns the path to usage.jsonl relative to dataDir.
func UsageLogPath(dataDir string) string {
	return filepath.Join(dataDir, "usage.jsonl")
}

// AppendUsageLog appends one entry to the JSONL log file (creates if needed).
func AppendUsageLog(path string, entry UsageEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// ReadUsageLog reads all entries from the JSONL log.
// Returns nil, nil if the file does not exist. Malformed lines are skipped.
func ReadUsageLog(path string) ([]UsageEntry, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []UsageEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e UsageEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, sc.Err()
}

// CallStats holds cumulative counters for a group of entries.
type CallStats struct {
	Calls        int
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

func (s *CallStats) add(e UsageEntry) {
	s.Calls++
	s.InputTokens += e.InputTokens
	s.OutputTokens += e.OutputTokens
	s.CostUSD += e.CostUSD
}

// DayStats is CallStats for a single calendar day.
type DayStats struct {
	Date  string // "2006-01-02"
	Stats CallStats
}

// AggregatedStats is the full breakdown returned by AggregateUsage.
type AggregatedStats struct {
	Total     CallStats
	ByProfile map[string]CallStats
	ByDay     []DayStats // chronological
}

// AggregateUsage filters entries by topic (empty = all) and last days (0 = all).
func AggregateUsage(entries []UsageEntry, topic string, days int) AggregatedStats {
	var cutoff time.Time
	if days > 0 {
		cutoff = time.Now().AddDate(0, 0, -days)
	}
	agg := AggregatedStats{ByProfile: make(map[string]CallStats)}
	dayMap := make(map[string]*CallStats)

	for _, e := range entries {
		if topic != "" && e.Topic != topic {
			continue
		}
		if days > 0 && e.Timestamp.Before(cutoff) {
			continue
		}
		agg.Total.add(e)
		ps := agg.ByProfile[e.Profile]
		ps.add(e)
		agg.ByProfile[e.Profile] = ps
		day := e.Timestamp.Format("2006-01-02")
		if dayMap[day] == nil {
			dayMap[day] = &CallStats{}
		}
		dayMap[day].add(e)
	}

	dayKeys := make([]string, 0, len(dayMap))
	for d := range dayMap {
		dayKeys = append(dayKeys, d)
	}
	sort.Strings(dayKeys)
	for _, d := range dayKeys {
		agg.ByDay = append(agg.ByDay, DayStats{Date: d, Stats: *dayMap[d]})
	}
	return agg
}
