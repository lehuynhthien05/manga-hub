package catalog_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"mangahub/internal/catalog"
	"mangahub/proto/mangaproto"
)

// client starts the real gRPC server over an in-memory listener.
func client(t *testing.T) (mangaproto.MangaServiceClient, *grpc.ClientConn) {
	t.Helper()
	store, _ := seededStore(t)
	srv, _ := catalog.NewGRPCServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	// NewClient defaults to the dns resolver; passthrough avoids resolving "bufnet".
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return mangaproto.NewMangaServiceClient(conn), conn
}

func TestGRPC_HappyPaths(t *testing.T) {
	c, conn := client(t)
	ctx := context.Background()

	got, err := c.GetManga(ctx, &mangaproto.GetMangaRequest{Id: "one-piece"})
	require.NoError(t, err)
	assert.Equal(t, int32(1100), got.GetManga().GetTotalChapters())

	res, err := c.SearchManga(ctx, &mangaproto.SearchMangaRequest{Query: "one"})
	require.NoError(t, err)
	assert.Equal(t, int32(2), res.GetTotal())

	up, err := c.UpdateChapters(ctx, &mangaproto.UpdateChaptersRequest{Id: "one-piece", TotalChapters: 1101})
	require.NoError(t, err)
	assert.Equal(t, int32(1100), up.GetPreviousTotalChapters())
	assert.Equal(t, int32(1101), up.GetManga().GetTotalChapters())

	hc, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: "mangaproto.MangaService"})
	require.NoError(t, err)
	assert.Equal(t, healthpb.HealthCheckResponse_SERVING, hc.GetStatus())
}

func TestGRPC_ErrorCodes(t *testing.T) {
	c, _ := client(t)
	ctx := context.Background()
	call := map[string]func() error{
		"get empty id": func() error { _, err := c.GetManga(ctx, &mangaproto.GetMangaRequest{}); return err },
		"get missing":  func() error { _, err := c.GetManga(ctx, &mangaproto.GetMangaRequest{Id: "nope"}); return err },
		"update lower": func() error {
			_, err := c.UpdateChapters(ctx, &mangaproto.UpdateChaptersRequest{Id: "one-piece", TotalChapters: 1})
			return err
		},
		"update zero": func() error {
			_, err := c.UpdateChapters(ctx, &mangaproto.UpdateChaptersRequest{Id: "one-piece"})
			return err
		},
		"search negative limit": func() error {
			_, err := c.SearchManga(ctx, &mangaproto.SearchMangaRequest{Limit: -1})
			return err
		},
		"search bad status": func() error {
			_, err := c.SearchManga(ctx, &mangaproto.SearchMangaRequest{Status: "cancelled"})
			return err
		},
	}
	want := map[string]codes.Code{
		"get empty id": codes.InvalidArgument, "get missing": codes.NotFound,
		"update lower": codes.FailedPrecondition, "update zero": codes.InvalidArgument,
		"search negative limit": codes.InvalidArgument, "search bad status": codes.InvalidArgument,
	}
	for name, f := range call {
		assert.Equal(t, want[name], status.Code(f()), name)
	}
}

func TestGRPC_CanceledContext(t *testing.T) {
	c, _ := client(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GetManga(ctx, &mangaproto.GetMangaRequest{Id: "one-piece"})
	assert.Equal(t, codes.Canceled, status.Code(err))
}
