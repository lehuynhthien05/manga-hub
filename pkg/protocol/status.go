package protocol

// Library statuses are hyphenated everywhere (API, SQL, TUI).
const (
	StatusPlanToRead = "plan-to-read"
	StatusReading    = "reading"
	StatusCompleted  = "completed"
)

func ValidStatus(s string) bool {
	switch s {
	case StatusPlanToRead, StatusReading, StatusCompleted:
		return true
	}
	return false
}

const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// Message types: the "type" field of every TCP, UDP and WebSocket message.
const (
	TypeAuth            = "auth"
	TypeAuthOK          = "auth_ok"
	TypeError           = "error"
	TypeProgressUpdated = "progress_updated"
	TypePing            = "ping"
	TypePong            = "pong"

	TypeSubscribe    = "subscribe"
	TypeSubscribed   = "subscribed"
	TypeUnsubscribe  = "unsubscribe"
	TypeUnsubscribed = "unsubscribed"
	TypeNewChapter   = "new_chapter"

	TypeJoin    = "join"
	TypeLeave   = "leave"
	TypeChat    = "chat"
	TypeSystem  = "system"
	TypeHistory = "history"
)
