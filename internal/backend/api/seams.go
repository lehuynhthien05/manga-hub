package api

import (
	"context"

	"mangahub/pkg/protocol"
)

// SearchParams are the validated query filters of GET /api/manga.
type SearchParams struct {
	Query, Genre, Status string
	Limit, Offset        int
}

// Catalog is implemented by catalogclient (gRPC). Handlers map its errors:
// NotFound→404, InvalidArgument→400, FailedPrecondition→409, Unavailable/DeadlineExceeded→503.
type Catalog interface {
	Search(ctx context.Context, p SearchParams) (protocol.SearchResponse, error)
	Get(ctx context.Context, id string) (protocol.Manga, error)
	UpdateChapters(ctx context.Context, id string, newTotal int) (m protocol.Manga, previousTotal int, err error)
}

// ProgressPublisher is implemented by the TCP hub. Call it only after the
// library write has committed; it must not block.
type ProgressPublisher interface {
	Publish(userID string, ev protocol.ProgressUpdated, originSession string)
}

// ChapterNotifier is implemented by the UDP server. The result counts
// datagrams attempted, not delivered; it never returns an error.
type ChapterNotifier interface {
	Notify(ev protocol.NewChapter) (sent int)
}

// CacheInvalidator is implemented by the optional Redis cache (Phase 12).
type CacheInvalidator interface {
	Invalidate(ctx context.Context, mangaID string)
}
