package core

import "io/fs"

// Topic holds a loaded topic's data.
type Topic struct {
	Name         string
	SystemPrompt string
	History      *History
}

// Store is the interface for topic persistence.
type Store interface {
	ListTopics() ([]string, error)

	LoadTopic(name string) (*Topic, error)
	SaveTopic(t *Topic) error

	LoadSystem(name string) (string, error)
	SaveSystem(name, system string) error

	LoadHistory(name string) (*History, error)
	SaveHistory(name string, h *History) error

	ClearHistory(name string) error
	DeleteTopic(name string) error

	// LoadSummary loads the cached summary. Returns ("", -1, nil) if none exists.
	// coversThrough is the index of the last Msg included in the summary.
	LoadSummary(topicName string) (text string, coversThrough int, err error)
	SaveSummary(topicName string, text string, coversThrough int) error

	// CreateResource copies a file into the topic's resources/ directory.
	CreateResource(topicName, sourcePath string) error
	// ListResources returns file info for all resources in the topic's resources/ directory.
	ListResources(topicName string) ([]fs.FileInfo, error)
	// DeleteResource removes a resource file by name from the topic's resources/ directory.
	DeleteResource(topicName, name string) error
}
