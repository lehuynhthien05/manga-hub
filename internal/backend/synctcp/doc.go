// Package synctcp is the TCP progress-sync hub: one JSON object per line, the
// first line must be auth, events go only to the same user's other sessions.
// It implements api.ProgressPublisher and must not import internal/catalog.
package synctcp
