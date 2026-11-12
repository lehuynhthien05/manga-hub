package protocol

type TCPAuth struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

type TCPAuthOK struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}

type TCPError struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

// ProgressUpdated is pushed to the same user's other sessions after a commit.
type ProgressUpdated struct {
	Type    string `json:"type"`
	MangaID string `json:"manga_id"`
	Chapter int    `json:"chapter"` // 0 is valid: no omitempty
	Version int    `json:"version"`
	Status  string `json:"status,omitempty"`
}

type TCPPing struct {
	Type string `json:"type"`
}

type TCPPong struct {
	Type string `json:"type"`
}
