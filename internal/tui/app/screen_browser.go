package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

const pageSize = 20

type Browser struct {
	a       *App
	root    *tview.Flex
	input   *tview.InputField
	banner  *tview.TextView
	table   *tview.Table
	details *tview.TextView

	items       []protocol.Manga
	query       string
	page, total int
	searched    bool
	seq         int                // only the newest search may render (stale responses are dropped)
	cancel      context.CancelFunc // cancels the superseded search
}

func newBrowser(a *App) *Browser {
	b := &Browser{a: a, input: tview.NewInputField().SetLabel("Search: "),
		banner: tview.NewTextView().SetDynamicColors(true), table: tview.NewTable().SetSelectable(true, false).SetFixed(1, 0),
		details: tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)}
	b.table.SetBorder(true).SetTitle(" Results ")
	b.details.SetBorder(true).SetTitle(" Details ")
	left := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(b.input, 1, 0, true).AddItem(b.banner, 1, 0, false).AddItem(b.table, 0, 1, false)
	b.root = tview.NewFlex().AddItem(left, 0, 3, true).AddItem(b.details, 0, 2, false)

	b.input.SetDoneFunc(func(k tcell.Key) {
		switch k {
		case tcell.KeyEnter:
			b.search(strings.TrimSpace(b.input.GetText()), 1)
			a.tv.SetFocus(b.table) // so a / n / p work right away
		case tcell.KeyTab, tcell.KeyEscape:
			a.tv.SetFocus(b.table)
		}
	})
	b.table.SetSelectionChangedFunc(func(row, _ int) { b.showDetails(b.selected()) })
	b.table.SetSelectedFunc(func(row, _ int) { b.refreshDetails() })
	b.table.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch {
		case ev.Key() == tcell.KeyTab:
			a.tv.SetFocus(b.details)
		case ev.Key() == tcell.KeyBacktab:
			a.tv.SetFocus(b.input)
		case ev.Rune() == '/':
			a.tv.SetFocus(b.input)
		case ev.Rune() == 'a':
			b.openAdd()
		case ev.Rune() == 'n' && b.page*pageSize < b.total:
			b.search(b.query, b.page+1)
		case ev.Rune() == 'p' && b.page > 1:
			b.search(b.query, b.page-1)
		default:
			return ev
		}
		return nil
	})
	b.details.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch {
		case ev.Key() == tcell.KeyTab || ev.Rune() == '/':
			a.tv.SetFocus(b.input)
		case ev.Key() == tcell.KeyBacktab || ev.Key() == tcell.KeyEscape:
			a.tv.SetFocus(b.table)
		case ev.Rune() == 'a':
			b.openAdd()
		default:
			return ev
		}
		return nil
	})
	return b
}

func (b *Browser) Name() string           { return "browser" }
func (b *Browser) Root() tview.Primitive  { return b.root }
func (b *Browser) Focus() tview.Primitive { return b.table }
func (b *Browser) Hints() string          { return "/ search · Tab pane · Enter details · a add · n/p page" }

func (b *Browser) OnShow(*App) {
	if !b.searched {
		b.search("", 1) // list the catalog on first visit
	}
}

// Find searches for q and focuses the results (used by the Library's Enter).
func (b *Browser) Find(q string) {
	b.input.SetText(q)
	b.a.Goto("browser")
	b.search(q, 1)
}

func (b *Browser) search(q string, page int) {
	b.searched = true
	b.seq++
	seq := b.seq
	if b.cancel != nil {
		b.cancel()
	}
	ctx, cancel := context.WithCancel(b.a.ctx)
	b.cancel = cancel
	b.a.Go(func() func(*App) {
		res, err := b.a.api.Search(ctx, q, page, pageSize)
		return func(a *App) {
			if seq != b.seq || errors.Is(err, context.Canceled) {
				return
			}
			b.render(q, res, err)
		}
	})
}

func (b *Browser) render(q string, res protocol.SearchResponse, err error) {
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) && ae.Status == http.StatusServiceUnavailable {
			b.banner.SetText("[red]Catalog service unavailable — press / to retry")
			return
		}
		b.banner.SetText("[red]" + tview.Escape(client.FriendlyMessage(err)))
		b.a.Fail(err)
		return
	}
	b.banner.Clear()
	b.items, b.query, b.page, b.total = res.Items, q, res.Page, res.Total
	b.table.Clear()
	for c, h := range []string{"Title", "Status", "Ch."} { // author is in Details: 4 columns do not fit at 80×24
		b.table.SetCell(0, c, tview.NewTableCell(h).SetSelectable(false).SetAttributes(tcell.AttrBold))
	}
	pages := max(1, (res.Total+pageSize-1)/pageSize)
	b.table.SetTitle(fmt.Sprintf(" Results · %d · page %d/%d ", res.Total, res.Page, pages))
	if len(res.Items) == 0 {
		b.table.SetCell(1, 0, tview.NewTableCell(tview.Escape("No manga found for '"+q+"'")).
			SetTextColor(tcell.ColorGray).SetSelectable(false))
		b.details.Clear()
		return
	}
	for i, m := range res.Items {
		b.table.SetCell(i+1, 0, tview.NewTableCell(tview.Escape(m.Title)).SetExpansion(1).SetMaxWidth(30))
		b.table.SetCell(i+1, 1, tview.NewTableCell(tview.Escape(m.Status)))
		b.table.SetCell(i+1, 2, tview.NewTableCell(strconv.Itoa(m.TotalChapters)).SetAlign(tview.AlignRight))
	}
	b.table.Select(1, 0).ScrollToBeginning()
	b.showDetails(b.selected())
}

func (b *Browser) selected() *protocol.Manga {
	row, _ := b.table.GetSelection()
	if row < 1 || row > len(b.items) {
		return nil
	}
	return &b.items[row-1]
}

// showDetails renders from the already-loaded row: instant, no request.
func (b *Browser) showDetails(m *protocol.Manga) {
	if m == nil {
		b.details.Clear()
		return
	}
	b.details.SetText(fmt.Sprintf("[::b]%s[::-]\n\n[gray]Author:[-]   %s\n[gray]Genres:[-]   %s\n[gray]Status:[-]   %s\n[gray]Chapters:[-] %d\n\n%s\n\n[gray]a: add to library",
		tview.Escape(m.Title), tview.Escape(m.Author), tview.Escape(strings.Join(m.Genres, ", ")),
		tview.Escape(m.Status), m.TotalChapters, tview.Escape(m.Description))).ScrollToBeginning()
}

func (b *Browser) refreshDetails() {
	m := b.selected()
	if m == nil {
		return
	}
	id, seq := m.ID, b.seq
	b.a.Go(func() func(*App) {
		fresh, err := b.a.api.GetManga(b.a.ctx, id)
		return func(a *App) {
			if err != nil {
				a.Fail(err)
				return
			}
			if cur := b.selected(); seq == b.seq && cur != nil && cur.ID == id {
				*cur = fresh
				b.showDetails(cur)
				a.Info("Details refreshed")
			}
		}
	})
}

func (b *Browser) openAdd() {
	m := b.selected()
	if m == nil {
		b.a.Info("Select a manga first")
		return
	}
	id, title := m.ID, m.Title
	statuses := []string{protocol.StatusPlanToRead, protocol.StatusReading, protocol.StatusCompleted}
	form := tview.NewForm().AddDropDown("Status", statuses, 0, nil)
	form.AddButton("Add", func() {
		_, status := form.GetFormItemByLabel("Status").(*tview.DropDown).GetCurrentOption()
		b.a.CloseModal("add")
		b.a.Go(func() func(*App) {
			_, err := b.a.api.AddToLibrary(b.a.ctx, id, status)
			return func(a *App) {
				var ae *client.APIError
				switch {
				case err == nil:
					a.Success("Added to library: " + title)
				case errors.As(err, &ae) && ae.Status == http.StatusConflict:
					a.Info("Already in your library")
				default:
					a.Fail(err)
				}
			}
		})
	}).AddButton("Cancel", func() { b.a.CloseModal("add") })
	form.SetCancelFunc(func() { b.a.CloseModal("add") })
	form.SetBorder(true).SetTitle(" Add " + tview.Escape(truncate(title, 30)) + " ")
	b.a.OpenModal("add", form, 50, 7)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
