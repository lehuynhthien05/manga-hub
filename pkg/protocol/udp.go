package protocol

type UDPSubscribe struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

type UDPSubscribed struct {
	Type string `json:"type"`
}

type UDPUnsubscribe struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

type UDPUnsubscribed struct {
	Type string `json:"type"`
}

// NewChapter is one datagram; keep it well under ~512 bytes.
type NewChapter struct {
	Type    string `json:"type"`
	MangaID string `json:"manga_id"`
	Title   string `json:"title,omitempty"`
	Chapter int    `json:"chapter"`
}

type UDPPing struct {
	Type string `json:"type"`
}

type UDPPong struct {
	Type string `json:"type"`
}

type UDPError struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}
