package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"mangahub/pkg/protocol"
)

type noopNotifier struct{}

func (noopNotifier) Notify(protocol.NewChapter) int { return 0 }

// release order: catalog UpdateChapters → cache invalidate → UDP notify.
// A catalog failure sends nothing; a UDP problem never fails the request.
func (h *handlers) release(c *gin.Context) {
	id := c.Param("id")
	if !slugRe.MatchString(id) {
		Fail(c, http.StatusBadRequest, protocol.CodeInvalidInput, "id must match [a-z0-9-]{1,64}")
		return
	}
	var req protocol.ReleaseRequest
	if c.Request.ContentLength != 0 && !bindJSON(c, &req) { // the body is optional
		return
	}
	ctx := c.Request.Context()
	var target int
	if req.Chapter != nil {
		if target = *req.Chapter; target <= 0 {
			Fail(c, http.StatusBadRequest, protocol.CodeInvalidInput, "chapter: must be at least 1")
			return
		}
	} else { // default: the next chapter
		m, err := h.Catalog.Get(ctx, id)
		if err != nil {
			h.writeCatalogErr(c, err)
			return
		}
		target = m.TotalChapters + 1
	}
	m, _, err := h.Catalog.UpdateChapters(ctx, id, target) // not higher than now → 409
	if err != nil {
		h.writeCatalogErr(c, err)
		return
	}
	if h.Cache != nil { // after the update succeeded, before anyone reacts to the alert
		h.Cache.Invalidate(ctx, id)
	}
	sent := h.Notifier.Notify(protocol.NewChapter{Type: protocol.TypeNewChapter, MangaID: m.ID, Title: m.Title, Chapter: m.TotalChapters})
	h.Log.Info("chapter released", "manga", m.ID, "chapter", m.TotalChapters, "udp_sent", sent)
	c.JSON(http.StatusOK, protocol.ReleaseResponse{Manga: m, Chapter: m.TotalChapters, UDPSent: sent})
}
