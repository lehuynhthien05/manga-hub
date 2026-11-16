package catalogclient

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mangahub/internal/backend/api"
)

func TestTranslate_EveryCode(t *testing.T) {
	assert.NoError(t, translate(nil))
	want := map[codes.Code]error{
		codes.NotFound:           api.ErrCatalogNotFound,
		codes.InvalidArgument:    api.ErrCatalogInvalid,
		codes.FailedPrecondition: api.ErrCatalogConflict,
		codes.Unavailable:        api.ErrCatalogUnavailable,
		codes.DeadlineExceeded:   api.ErrCatalogUnavailable,
		codes.Canceled:           context.Canceled,
	}
	for c := codes.OK + 1; c <= codes.Unauthenticated; c++ {
		got := translate(status.Error(c, "x"))
		if w, ok := want[c]; ok {
			assert.ErrorIs(t, got, w, c.String())
			continue
		}
		// Everything else is an unexpected failure (→ 502), never a domain error.
		for _, d := range []error{api.ErrCatalogNotFound, api.ErrCatalogInvalid, api.ErrCatalogConflict, api.ErrCatalogUnavailable} {
			assert.False(t, errors.Is(got, d), c.String())
		}
	}
	assert.Error(t, translate(status.Error(codes.Code(99), "unknown")))
}
