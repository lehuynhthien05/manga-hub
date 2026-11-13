package protocol

import "regexp"

var roomRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

func ValidRoom(name string) bool { return roomRe.MatchString(name) }

type WSJoin struct {
	Type string `json:"type"`
	Room string `json:"room"`
}

type WSLeave struct {
	Type string `json:"type"`
	Room string `json:"room"`
}

// WSChat goes both ways; the server fills User and TS before broadcasting.
type WSChat struct {
	Type string `json:"type"`
	Room string `json:"room"`
	User string `json:"user,omitempty"`
	Text string `json:"text"`
	TS   int64  `json:"ts,omitempty"`
}

type WSSystem struct {
	Type string `json:"type"`
	Room string `json:"room"`
	Text string `json:"text"`
}

type WSHistoryItem struct {
	User string `json:"user"`
	Text string `json:"text"`
	TS   int64  `json:"ts"`
}

type WSHistory struct {
	Type     string          `json:"type"`
	Room     string          `json:"room"`
	Messages []WSHistoryItem `json:"messages"`
}

type WSError struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}
