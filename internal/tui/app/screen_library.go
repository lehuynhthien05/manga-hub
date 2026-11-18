package app

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

type LibraryScreen struct {
	a     *App
	table *tview.Table
	ids   []string // manga id per table row (row 1 = ids[0])
}

func newLibraryScreen(a *App) *LibraryScreen {
	l := &LibraryScreen{a: a, table: tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)}
	l.table.SetBorder(true).SetTitle(" My Library ")
	l.table.SetSelectedFunc(func(int, int) {
		if id := l.selected(); id != "" {
			a.browser.Find(id)
		}
	})
	l.table.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'u':
			l.openChapter()
		case 's':
			l.openStatus()
		case 'r':
			l.refresh()
		default:
			return ev
		}
		return nil
	})
	return l
}

func (l *LibraryScreen) Name() string           { return "library" }
func (l *LibraryScreen) Root() tview.Primitive  { return l.table }
func (l *LibraryScreen) Focus() tview.Primitive { return l.table }
func (l *LibraryScreen) Hints() string          { return "u chapter · s status · r refresh · Enter open" }
func (l *LibraryScreen) OnShow(*App)            { l.refresh() }

func (l *LibraryScreen) refresh() {
	l.a.Go(func() func(*App) {
		res, err := l.a.api.ListLibrary(l.a.ctx)
		return func(a *App) {
			if err != nil {
				a.Fail(err)
				return
			}
			a.library.Replace(res.Items)
			l.render()
			if res.Catalog == protocol.CatalogUnavailable {
				a.Error("Catalog unavailable — showing ids and saved progress only")
			}
		}
	})
}

// render draws the table from the model only.
func (l *LibraryScreen) render() {
	keep := l.selected()
	l.table.Clear()
	for c, h := range []string{"Title", "Status", "Progress", "Ver", "Updated"} {
		l.table.SetCell(0, c, tview.NewTableCell(h).SetSelectable(false).SetAttributes(tcell.AttrBold))
	}
	items := l.a.library.Items()
	l.ids = l.ids[:0]
	if len(items) == 0 {
		l.table.SetCell(1, 0, tview.NewTableCell("Your library is empty — press 1 to find manga").
			SetTextColor(tcell.ColorGray).SetSelectable(false))
		return
	}
	now := time.Now()
	for i, it := range items {
		title := it.MangaID
		if it.Manga != nil {
			title = it.Manga.Title
		}
		l.ids = append(l.ids, it.MangaID)
		l.table.SetCell(i+1, 0, tview.NewTableCell(tview.Escape(title)).SetExpansion(1).SetMaxWidth(36))
		l.table.SetCell(i+1, 1, tview.NewTableCell(it.Status))
		l.table.SetCell(i+1, 2, tview.NewTableCell(progress(it)).SetAlign(tview.AlignRight))
		l.table.SetCell(i+1, 3, tview.NewTableCell(strconv.Itoa(it.Version)).SetAlign(tview.AlignRight))
		l.table.SetCell(i+1, 4, tview.NewTableCell(age(it.UpdatedAt, now)))
	}
	row := slices.Index(l.ids, keep) + 1
	l.table.Select(max(row, 1), 0)
}

func (l *LibraryScreen) selected() string {
	row, _ := l.table.GetSelection()
	if row < 1 || row > len(l.ids) {
		return ""
	}
	return l.ids[row-1]
}

func (l *LibraryScreen) openChapter() {
	it, ok := l.a.library.Get(l.selected())
	if !ok {
		return
	}
	// A plain field, not a Form: a Form moves focus to its buttons after Enter, so the
	// user could not type a correction after an error.
	msg := tview.NewTextView().SetDynamicColors(true).SetText("[gray]Enter save · Esc cancel")
	field := tview.NewInputField().SetLabel("Chapter ").SetText(strconv.Itoa(it.CurrentChapter)).
		SetFieldWidth(8).SetAcceptanceFunc(tview.InputFieldInteger)
	field.SetDoneFunc(func(k tcell.Key) {
		if k == tcell.KeyEscape {
			l.a.CloseModal("chapter")
			return
		}
		if k != tcell.KeyEnter {
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(field.GetText()))
		switch {
		case err != nil || n < 0:
			msg.SetText("[red]Enter a chapter number of 0 or more")
		case it.Manga != nil && n > it.Manga.TotalChapters:
			msg.SetText(fmt.Sprintf("[red]At most %d chapters", it.Manga.TotalChapters))
		default:
			l.update(it.MangaID, &n, nil, msg, "chapter")
		}
	})
	box := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 1, 0, false).AddItem(field, 1, 0, true).AddItem(nil, 1, 0, false).AddItem(msg, 2, 0, false)
	box.SetBorder(true).SetTitle(" Update chapter · " + progress(it) + " ")
	l.a.OpenModal("chapter", box, 52, 7)
}

func (l *LibraryScreen) openStatus() {
	it, ok := l.a.library.Get(l.selected())
	if !ok {
		return
	}
	statuses := []string{protocol.StatusPlanToRead, protocol.StatusReading, protocol.StatusCompleted}
	msg := tview.NewTextView().SetDynamicColors(true)
	form := tview.NewForm().AddDropDown("Status", statuses, max(slices.Index(statuses, it.Status), 0), nil)
	form.AddButton("Save", func() {
		_, s := form.GetFormItemByLabel("Status").(*tview.DropDown).GetCurrentOption()
		l.update(it.MangaID, nil, &s, msg, "status")
	}).AddButton("Cancel", func() { l.a.CloseModal("status") })
	form.SetCancelFunc(func() { l.a.CloseModal("status") })
	box := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(form, 0, 1, true).AddItem(msg, 2, 0, false)
	box.SetBorder(true).SetTitle(" Change status ")
	l.a.OpenModal("status", box, 52, 10)
}

// update sends the change; a server 400 stays inside the modal so the user can correct it.
func (l *LibraryScreen) update(id string, chapter *int, status *string, msg *tview.TextView, modal string) {
	msg.SetText("[yellow]Saving…")
	session := l.a.sessionID
	l.a.Go(func() func(*App) {
		it, err := l.a.api.UpdateProgress(l.a.ctx, id, chapter, status, session)
		return func(a *App) {
			var ae *client.APIError
			switch {
			case err == nil:
				a.CloseModal(modal)
				a.library.Upsert(it)
				l.render()
				a.Success(fmt.Sprintf("Saved: %s, version %d", progress(mustGet(a.library, id)), it.Version))
			case errors.As(err, &ae) && ae.Status == http.StatusBadRequest:
				msg.SetText("[red]" + tview.Escape(ae.Message))
			case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
				a.CloseModal(modal)
				a.Error("No longer in your library")
				l.refresh()
			default:
				msg.SetText("[red]" + tview.Escape(client.FriendlyMessage(err)))
				if client.IsUnauthorized(err) {
					a.Fail(err)
				}
			}
		}
	})
}

func mustGet(m *LibraryModel, id string) protocol.LibraryItem {
	it, _ := m.Get(id)
	return it
}
