package catalogclient

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"mangahub/internal/backend/api"
	"mangahub/pkg/protocol"
	"mangahub/proto/mangaproto"
)

var _ api.Catalog = (*Client)(nil)

// Reconnect backoff. gRPC's default grows towards 120 s, so after a catalog
// restart the backend would keep answering 503 for minutes; capping it at 2 s
// makes recovery quick. ConnectParams is marked EXPERIMENTAL in grpc-go: if an
// upgrade removes it, call conn.ResetConnectBackoff() after a failed call instead.
const (
	backoffBase       = 100 * time.Millisecond
	backoffMultiplier = 1.6
	backoffJitter     = 0.2
	backoffMax        = 2 * time.Second
	minConnectTimeout = time.Second
	healthTimeout     = 500 * time.Millisecond
	healthService     = "mangaproto.MangaService"
)

// Client is the backend's gRPC client for the catalog. It implements api.Catalog
// and turns gRPC status codes into the api.ErrCatalog* errors.
type Client struct {
	conn    *grpc.ClientConn
	rpc     mangaproto.MangaServiceClient
	health  healthpb.HealthClient
	timeout time.Duration
}

// New does not dial: the connection is made lazily, so the backend may start before the catalog.
func New(addr string, timeout time.Duration) (*Client, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.Config{BaseDelay: backoffBase, Multiplier: backoffMultiplier, Jitter: backoffJitter, MaxDelay: backoffMax},
			MinConnectTimeout: minConnectTimeout,
		}))
	if err != nil {
		return nil, fmt.Errorf("catalog client: %w", err)
	}
	return &Client{conn: conn, rpc: mangaproto.NewMangaServiceClient(conn),
		health: healthpb.NewHealthClient(conn), timeout: timeout}, nil
}

// Close closes the gRPC connection.
func (c *Client) Close() error { return c.conn.Close() }

// call runs fn under the GRPC_TIMEOUT deadline, derived from the request
// context so a disconnected HTTP client also cancels the RPC.
func (c *Client) call(ctx context.Context, fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return translate(fn(ctx))
}

// Search calls SearchManga under the GRPC_TIMEOUT deadline.
func (c *Client) Search(ctx context.Context, p api.SearchParams) (protocol.SearchResponse, error) {
	var res *mangaproto.SearchMangaResponse
	err := c.call(ctx, func(ctx context.Context) (err error) {
		res, err = c.rpc.SearchManga(ctx, &mangaproto.SearchMangaRequest{Query: p.Query, Genre: p.Genre,
			Status: p.Status, Limit: int32(p.Limit), Offset: int32(p.Offset)})
		return err
	})
	if err != nil {
		return protocol.SearchResponse{}, err
	}
	out := protocol.SearchResponse{Items: make([]protocol.Manga, 0, len(res.GetItems())), Total: int(res.GetTotal())}
	for _, m := range res.GetItems() {
		out.Items = append(out.Items, fromProto(m))
	}
	return out, nil
}

// Get calls GetManga under the GRPC_TIMEOUT deadline.
func (c *Client) Get(ctx context.Context, id string) (protocol.Manga, error) {
	var res *mangaproto.GetMangaResponse
	err := c.call(ctx, func(ctx context.Context) (err error) {
		res, err = c.rpc.GetManga(ctx, &mangaproto.GetMangaRequest{Id: id})
		return err
	})
	if err != nil {
		return protocol.Manga{}, err
	}
	return fromProto(res.GetManga()), nil
}

// UpdateChapters calls UpdateChapters and also returns the previous total.
// A total that is not higher than the current one maps to ErrCatalogConflict.
func (c *Client) UpdateChapters(ctx context.Context, id string, newTotal int) (protocol.Manga, int, error) {
	var res *mangaproto.UpdateChaptersResponse
	err := c.call(ctx, func(ctx context.Context) (err error) {
		res, err = c.rpc.UpdateChapters(ctx, &mangaproto.UpdateChaptersRequest{Id: id, TotalChapters: int32(newTotal)})
		return err
	})
	if err != nil {
		return protocol.Manga{}, 0, err
	}
	return fromProto(res.GetManga()), int(res.GetPreviousTotalChapters()), nil
}

// Healthy asks the standard gRPC health service, with its own short deadline.
func (c *Client) Healthy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	res, err := c.health.Check(ctx, &healthpb.HealthCheckRequest{Service: healthService})
	return err == nil && res.GetStatus() == healthpb.HealthCheckResponse_SERVING
}

// translate is the single gRPC → domain error mapping.
func translate(err error) error {
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.NotFound:
		return api.ErrCatalogNotFound
	case codes.InvalidArgument:
		return api.ErrCatalogInvalid
	case codes.FailedPrecondition:
		return api.ErrCatalogConflict
	case codes.Unavailable, codes.DeadlineExceeded:
		return api.ErrCatalogUnavailable
	case codes.Canceled:
		return context.Canceled
	default:
		return fmt.Errorf("catalog: %w", err)
	}
}

func fromProto(m *mangaproto.Manga) protocol.Manga {
	genres := m.GetGenres()
	if genres == nil {
		genres = []string{} // JSON [] rather than null
	}
	return protocol.Manga{ID: m.GetId(), Title: m.GetTitle(), Author: m.GetAuthor(), Genres: genres,
		Status: m.GetStatus(), TotalChapters: int(m.GetTotalChapters()), Description: m.GetDescription()}
}
