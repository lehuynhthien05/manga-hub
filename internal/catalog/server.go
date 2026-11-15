package catalog

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"mangahub/proto/mangaproto"
)

// Server implements mangaproto.MangaService on top of Store.
type Server struct {
	mangaproto.UnimplementedMangaServiceServer
	store *Store
	log   *slog.Logger
}

// NewGRPCServer builds a gRPC server with the catalog, health and reflection
// services registered. The returned health server is flipped to NOT_SERVING on stop.
func NewGRPCServer(store *Store, log *slog.Logger) (*grpc.Server, *health.Server) {
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(recoveryInterceptor(log), loggingInterceptor(log)))
	mangaproto.RegisterMangaServiceServer(srv, &Server{store: store, log: log})
	hs := health.NewServer()
	hs.SetServingStatus("mangaproto.MangaService", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, hs)
	reflection.Register(srv) // lets grpcurl work without the .proto
	return srv, hs
}

var validStatus = map[string]bool{"": true, "ongoing": true, "completed": true, "hiatus": true}

func (s *Server) SearchManga(ctx context.Context, req *mangaproto.SearchMangaRequest) (*mangaproto.SearchMangaResponse, error) {
	if !validStatus[req.GetStatus()] {
		return nil, status.Errorf(codes.InvalidArgument, "unknown status %q", req.GetStatus())
	}
	items, total, err := s.store.Search(ctx, SearchParams{
		Query: req.GetQuery(), Genre: req.GetGenre(), Status: req.GetStatus(),
		Limit: int(req.GetLimit()), Offset: int(req.GetOffset()),
	})
	if err != nil {
		return nil, s.toStatus(err)
	}
	resp := &mangaproto.SearchMangaResponse{Total: int32(total)}
	for _, m := range items {
		resp.Items = append(resp.Items, toProto(m))
	}
	return resp, nil
}

func (s *Server) GetManga(ctx context.Context, req *mangaproto.GetMangaRequest) (*mangaproto.GetMangaResponse, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	m, err := s.store.Get(ctx, req.GetId())
	if err != nil {
		return nil, s.toStatus(err)
	}
	return &mangaproto.GetMangaResponse{Manga: toProto(m)}, nil
}

func (s *Server) UpdateChapters(ctx context.Context, req *mangaproto.UpdateChaptersRequest) (*mangaproto.UpdateChaptersResponse, error) {
	m, prev, err := s.store.UpdateChapters(ctx, req.GetId(), int(req.GetTotalChapters()))
	if err != nil {
		return nil, s.toStatus(err)
	}
	return &mangaproto.UpdateChaptersResponse{Manga: toProto(m), PreviousTotalChapters: int32(prev)}, nil
}

// toStatus is the single place that maps store errors to gRPC codes.
func (s *Server) toStatus(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, "manga not found")
	case errors.Is(err, ErrInvalid):
		return status.Error(codes.InvalidArgument, "invalid argument: id is required, limit/offset must be >= 0, total_chapters must be > 0")
	case errors.Is(err, ErrNotIncreasing):
		return status.Error(codes.FailedPrecondition, "new total_chapters must be greater than the current one")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	}
	s.log.Error("catalog store error", "err", err) // details stay in the log
	return status.Error(codes.Internal, "internal error")
}

func toProto(m Manga) *mangaproto.Manga {
	return &mangaproto.Manga{
		Id: m.ID, Title: m.Title, Author: m.Author, Genres: m.Genres,
		Status: m.Status, TotalChapters: int32(m.TotalChapters), Description: m.Description,
	}
}

func recoveryInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic in handler", "method", info.FullMethod, "panic", r, "stack", string(debug.Stack()))
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return h(ctx, req)
	}
}

func loggingInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := h(ctx, req)
		code := status.Code(err)
		lv := slog.LevelInfo
		if err != nil {
			lv = slog.LevelWarn
		}
		log.Log(ctx, lv, "rpc", "method", info.FullMethod, "code", code.String(), "dur", time.Since(start))
		return resp, err
	}
}
