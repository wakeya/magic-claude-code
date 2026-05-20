package session

import "time"

type Project struct {
	Path         string    `json:"path"`
	Name         string    `json:"name"`
	SessionCount int       `json:"session_count"`
	LastActiveAt time.Time `json:"last_active_at"`
}

type Session struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	ProjectPath  string    `json:"project_path"`
	SourcePath   string    `json:"source_path"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	MessageCount int       `json:"message_count"`
}

type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"ts,omitempty"`
}

type SessionDetail struct {
	Session  Session   `json:"session"`
	Messages []Message `json:"messages"`
}

type CleanupHint struct {
	ProjectPath        string `json:"project_path"`
	PreviewCommand     string `json:"preview_command"`
	InteractiveCommand string `json:"interactive_command"`
	Note               string `json:"note"`
}
