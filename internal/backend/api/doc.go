// Package api holds the Gin HTTP handlers of the backend and the small
// interfaces they consume (Catalog, ProgressPublisher, ChapterNotifier).
// main wires the concrete types. It must not import internal/catalog or
// the concrete catalogclient, synctcp or notifyudp packages.
package api
