package models

import "time"

// Manga represents a manga series in the database
type Manga struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Author        string   `json:"author"`
	Artist        string   `json:"artist,omitempty"`
	Genres        []string `json:"genres"`
	Status        string   `json:"status"` // ongoing, completed, hiatus
	TotalChapters int      `json:"total_chapters"`
	Description   string   `json:"description"`
	CoverURL      string   `json:"cover_url,omitempty"`
	Year          int      `json:"year,omitempty"`
	Rating        float64  `json:"rating,omitempty"`
}

// User represents an authenticated user
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// UserProgress represents the user's reading status for a manga
type UserProgress struct {
	UserID         string     `json:"user_id"`
	MangaID        string     `json:"manga_id"`
	MangaTitle     string     `json:"manga_title,omitempty"`
	CurrentChapter int        `json:"current_chapter"`
	TotalChapters  int        `json:"total_chapters,omitempty"`
	Status         string     `json:"status"` // reading, completed, plan-to-read, on-hold, dropped
	Rating         int        `json:"rating,omitempty"`
	Notes          string     `json:"notes,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	UpdatedAt      time.Time  `json:"last_updated"`
}

// ProgressUpdate represents a synchronization payload sent over TCP / channel
type ProgressUpdate struct {
	Type      string `json:"type,omitempty"` // "progress_update", "sync_ack", etc.
	UserID    string `json:"user_id"`
	MangaID   string `json:"manga_id"`
	Chapter   int    `json:"chapter"`
	Volume    int    `json:"volume,omitempty"`
	Notes     string `json:"notes,omitempty"`
	DeviceID  string `json:"device_id,omitempty"`
	Timestamp int64  `json:"timestamp"`
	Force     bool   `json:"force,omitempty"`
}

// ConflictResolution represents TCP sync conflict resolution details
type ConflictResolution struct {
	Strategy   string `json:"strategy"` // "last_write_wins", "merge", "user_choice"
	Timestamp  int64  `json:"timestamp"`
	DeviceID   string `json:"device_id"`
	Resolution string `json:"resolution"`
}

// Notification represents a UDP notification packet
type Notification struct {
	Type      string `json:"type"` // "chapter_release", "system_alert", "ping", "pong"
	MangaID   string `json:"manga_id,omitempty"`
	Title     string `json:"title,omitempty"`
	Chapter   int    `json:"chapter,omitempty"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

// UDPRegistration represents a client registration payload for UDP notifications
type UDPRegistration struct {
	Type        string                 `json:"type"` // "register", "unregister", "ping"
	UserID      string                 `json:"user_id"`
	Preferences map[string]interface{} `json:"preferences,omitempty"`
}

// ChatMessage represents a WebSocket message
type ChatMessage struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"` // "message", "join", "leave", "users", "history", "pm", "error"
	Room      string `json:"room,omitempty"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Target    string `json:"target,omitempty"` // For private messages
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

// Review represents a manga review and rating
type Review struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	MangaID   string `json:"manga_id"`
	Rating    int    `json:"rating"` // 1-10
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
	Helpful   int    `json:"helpful"`
}

// ReadingStats represents calculated reading analytics
type ReadingStats struct {
	TotalChaptersRead   int            `json:"total_chapters_read"`
	ReadingTimeMinutes  int            `json:"reading_time_minutes"`
	FavoriteGenres      []string       `json:"favorite_genres"`
	ReadingStreak       int            `json:"reading_streak"`
	MonthlyGoals        map[string]int `json:"monthly_goals,omitempty"`
	EstimatedCompletion string         `json:"estimated_completion,omitempty"`
}

// SearchFilters represents query parameters for manga searching
type SearchFilters struct {
	Query       string `json:"query"`
	Genre       string `json:"genre"`
	Status      string `json:"status"`
	Author      string `json:"author"`
	YearFrom    int    `json:"year_from"`
	YearTo      int    `json:"year_to"`
	MinChapters int    `json:"min_chapters"`
	SortBy      string `json:"sort_by"` // "popularity", "rating", "recent", "title"
	Order       string `json:"order"`   // "asc", "desc"
	Page        int    `json:"page"`
	Limit       int    `json:"limit"`
}
