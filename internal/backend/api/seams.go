package api

import (
	"context"
	"errors"

	"mangahub/pkg/protocol"
)

// SearchParams are the validated query filters of GET /api/manga.
type SearchParams struct {
	Query, Genre, Status string
	Limit, Offset        int
}

// Domain errors a Catalog returns. They live here (the consumer) so that
// catalogclient imports api, and api never imports gRPC.
var (
	ErrCatalogNotFound    = errors.New("catalog: not found")           // gRPC NotFound
	ErrCatalogInvalid     = errors.New("catalog: invalid argument")    // gRPC InvalidArgument
	ErrCatalogConflict    = errors.New("catalog: precondition failed") // gRPC FailedPrecondition
	ErrCatalogUnavailable = errors.New("catalog: unavailable")         // gRPC Unavailable / DeadlineExceeded
)

// Catalog is implemented by catalogclient (gRPC). Handlers map its errors
// with mapCatalogErr: NotFound→404, Invalid→400, Conflict→409, Unavailable→503, other→502.
type Catalog interface {
	Search(ctx context.Context, p SearchParams) (protocol.SearchResponse, error)
	Get(ctx context.Context, id string) (protocol.Manga, error)
	UpdateChapters(ctx context.Context, id string, newTotal int) (m protocol.Manga, previousTotal int, err error)
	// Healthy reports whether the catalog answers its health check; it is bounded and never blocks long.
	Healthy(ctx context.Context) bool
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
