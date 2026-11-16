package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"mangahub/internal/auth"
	"mangahub/internal/backend/store"
	"mangahub/pkg/protocol"
)

type noopPublisher struct{}

func (noopPublisher) Publish(string, protocol.ProgressUpdated, string) {}

const enrichWorkers = 8 // at most 8 concurrent GetManga calls per library page

func toItem(it store.LibraryItem, m *protocol.Manga) protocol.LibraryItem {
	return protocol.LibraryItem{MangaID: it.MangaID, Status: it.Status, CurrentChapter: it.CurrentChapter,
		Version: it.Version, UpdatedAt: it.UpdatedAt, Manga: m}
}

func (h *handlers) addLibrary(c *gin.Context) {
	var req protocol.AddLibraryRequest
	if !bindJSON(c, &req) {
		return
	}
	if req.Status == "" {
		req.Status = protocol.StatusPlanToRead
	}
	switch {
	case !slugRe.MatchString(req.MangaID):
		Fail(c, http.StatusBadRequest, protocol.CodeInvalidInput, "manga_id: must match [a-z0-9-]{1,64}")
		return
	case !protocol.ValidStatus(req.Status):
		Fail(c, http.StatusBadRequest, protocol.CodeInvalidInput, "status: must be plan-to-read, reading or completed")
		return
	}
	m, err := h.Catalog.Get(c.Request.Context(), req.MangaID) // the manga must exist (404) and the catalog be up (503)
	if err != nil {
		h.writeCatalogErr(c, err)
		return
	}
	// shortcut: new entries start at chapter 0 because AddLibraryRequest has no chapter; set it with PUT …/progress.
	it, err := h.Store.AddLibrary(c.Request.Context(), auth.UserID(c), req.MangaID, req.Status, 0)
	if errors.Is(err, store.ErrAlreadyInLibrary) {
		Fail(c, http.StatusConflict, protocol.CodeConflict, "manga already in library")
		return
	}
	if err != nil {
		h.internal(c, err)
		return
	}
	c.JSON(http.StatusCreated, toItem(it, &m))
}

// listLibrary always returns the rows; catalog data is best-effort (decision D11).
func (h *handlers) listLibrary(c *gin.Context) {
	rows, err := h.Store.ListLibrary(c.Request.Context(), auth.UserID(c))
	if err != nil {
		h.internal(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), h.CatalogTimeout) // one deadline for the whole page
	defer cancel()
	items := make([]protocol.LibraryItem, len(rows))
	sem := make(chan struct{}, enrichWorkers)
	var wg sync.WaitGroup
	var down atomic.Bool
	for i, row := range rows {
		items[i] = toItem(row, nil)
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m, err := h.Catalog.Get(ctx, row.MangaID)
			switch {
			case err == nil:
				items[i].Manga = &m // each goroutine writes only its own index: no data race
			case errors.Is(err, ErrCatalogUnavailable):
				down.Store(true)
			} // NotFound (manga removed from the catalog): leave Manga nil
		}()
	}
	wg.Wait()
	state := protocol.CatalogOK
	if down.Load() {
		state = protocol.CatalogUnavailable
	}
	c.JSON(http.StatusOK, protocol.LibraryResponse{Items: items, Catalog: state})
}

// updateProgress order: shape (400) → ownership (404) → catalog total (400/404/503) → write → publish after commit.
func (h *handlers) updateProgress(c *gin.Context) {
	var req protocol.UpdateProgressRequest
	if !bindJSON(c, &req) {
		return
	}
	switch {
	case req.CurrentChapter == nil && req.Status == nil:
		Fail(c, http.StatusBadRequest, protocol.CodeInvalidInput, "body: current_chapter or status is required")
		return
	case req.Status != nil && !protocol.ValidStatus(*req.Status):
		Fail(c, http.StatusBadRequest, protocol.CodeInvalidInput, "status: must be plan-to-read, reading or completed")
		return
	case req.CurrentChapter != nil && *req.CurrentChapter < 0:
		Fail(c, http.StatusBadRequest, protocol.CodeInvalidInput, "current_chapter: must be at least 0")
		return
	}
	ctx, userID, mangaID := c.Request.Context(), auth.UserID(c), c.Param("mangaId")
	if _, err := h.Store.GetLibrary(ctx, userID, mangaID); err != nil {
		h.libraryErr(c, err)
		return
	}
	var manga *protocol.Manga
	if req.CurrentChapter != nil {
		m, err := h.Catalog.Get(ctx, mangaID)
		if err != nil {
			h.writeCatalogErr(c, err)
			return
		}
		if *req.CurrentChapter > m.TotalChapters {
			Fail(c, http.StatusBadRequest, protocol.CodeInvalidInput,
				fmt.Sprintf("current_chapter: %d exceeds total_chapters (%d)", *req.CurrentChapter, m.TotalChapters))
			return
		}
		manga = &m
	}
	it, err := h.Store.UpdateProgress(ctx, userID, mangaID, req.CurrentChapter, req.Status)
	if err != nil {
		h.libraryErr(c, err) // ErrNotInLibrary here means a concurrent delete
		return
	}
	// The UPDATE has committed; Publish only enqueues and returns immediately.
	h.Publisher.Publish(userID, protocol.ProgressUpdated{Type: protocol.TypeProgressUpdated, MangaID: it.MangaID,
		Chapter: it.CurrentChapter, Version: it.Version, Status: it.Status}, c.GetHeader("X-Session-ID"))
	c.JSON(http.StatusOK, toItem(it, manga))
}

func (h *handlers) libraryErr(c *gin.Context, err error) {
	if errors.Is(err, store.ErrNotInLibrary) {
		Fail(c, http.StatusNotFound, protocol.CodeNotFound, "manga not in your library")
		return
	}
	h.internal(c, err)
}
