// Package app holds the tview screens and the single UI event dispatcher.
// Only the UI goroutine touches widgets; network goroutines post events.
// It must not import internal/backend or internal/catalog.
package app
