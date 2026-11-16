package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"mangahub/pkg/protocol"
)

var (
	slugRe        = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
	mangaStatuses = map[string]bool{"": true, "ongoing": true, "completed": true, "hiatus": true}
)

const (
	defaultLimit = 20
	maxLimit     = 100
	maxQueryLen  = 100 // runes
	maxGenreLen  = 40
)

func (h *handlers) searchManga(c *gin.Context) {
	p, page, err := parseSearchQuery(c)
	if err != nil {
		Fail(c, http.StatusBadRequest, protocol.CodeInvalidInput, err.Error())
		return
	}
	res, err := h.Catalog.Search(c.Request.Context(), p)
	if err != nil {
		h.writeCatalogErr(c, err)
		return
	}
	res.Page, res.Limit = page, p.Limit
	c.JSON(http.StatusOK, res)
}

func (h *handlers) getManga(c *gin.Context) {
	id := c.Param("id")
	if !slugRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, protocol.CodeInvalidInput, "id must match [a-z0-9-]{1,64}")
		return
	}
	m, err := h.Catalog.Get(c.Request.Context(), id)
	if err != nil {
		h.writeCatalogErr(c, err)
		return
	}
	c.JSON(http.StatusOK, m)
}

// parseSearchQuery validates q, genre, status, limit and page; the error names the parameter.
func parseSearchQuery(c *gin.Context) (SearchParams, int, error) {
	p := SearchParams{
		Query:  strings.TrimSpace(c.Query("q")),
		Genre:  strings.TrimSpace(c.Query("genre")),
		Status: c.Query("status"),
		Limit:  defaultLimit,
	}
	page := 1
	switch {
	case utf8.RuneCountInString(p.Query) > maxQueryLen:
		return p, 0, fmt.Errorf("q: at most %d characters", maxQueryLen)
	case len(p.Genre) > maxGenreLen:
		return p, 0, fmt.Errorf("genre: at most %d characters", maxGenreLen)
	case !mangaStatuses[p.Status]:
		return p, 0, errors.New("status: must be ongoing, completed or hiatus")
	}
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			return p, 0, fmt.Errorf("limit: must be an integer from 1 to %d", maxLimit)
		}
		p.Limit = n
	}
	if v := c.Query("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return p, 0, errors.New("page: must be an integer of at least 1")
		}
		page = n
	}
	p.Offset = (page - 1) * p.Limit
	return p, page, nil
}

// mapCatalogErr is the single domain → HTTP mapping used by every handler.
func mapCatalogErr(err error) (status int, code, msg string) {
	switch {
	case errors.Is(err, ErrCatalogNotFound):
		return http.StatusNotFound, protocol.CodeNotFound, "manga not found"
	case errors.Is(err, ErrCatalogInvalid):
		return http.StatusBadRequest, protocol.CodeInvalidInput, "invalid request for the catalog"
	case errors.Is(err, ErrCatalogConflict):
		return http.StatusConflict, protocol.CodeConflict, "catalog precondition failed"
	case errors.Is(err, ErrCatalogUnavailable):
		return http.StatusServiceUnavailable, protocol.CodeCatalogUnavailable, "catalog service unavailable"
	default:
		return http.StatusBadGateway, protocol.CodeBadGateway, "catalog error"
	}
}

func (h *handlers) writeCatalogErr(c *gin.Context, err error) {
	status, code, msg := mapCatalogErr(err)
	switch {
	case errors.Is(err, context.Canceled):
		h.Log.Debug("catalog call canceled by client", "path", c.FullPath())
	case status >= 500:
		h.Log.Warn("catalog call failed", "path", c.FullPath(), "err", err)
	}
	Fail(c, status, code, msg)
}
