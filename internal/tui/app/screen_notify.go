package app

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"mangahub/pkg/protocol"
)

const maxNotices = 200

type notice struct {
	at time.Time
	ev protocol.NewChapter
}

type NotifyScreen struct {
	a      *App
	root   *tview.Flex
	state  *tview.TextView
	table  *tview.Table
	items  []notice // newest first
	unread int
}

func newNotifyScreen(a *App) *NotifyScreen {
	n := &NotifyScreen{a: a, state: tview.NewTextView().SetDynamicColors(true),
		table: tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)}
	n.table.SetBorder(true).SetTitle(" Notifications (UDP, best-effort) ")
	n.root = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(n.state, 1, 0, false).AddItem(n.table, 0, 1, true)
	n.table.SetSelectedFunc(func(row, _ int) {
		if row >= 1 && row <= len(n.items) {
			a.browser.Find(n.items[row-1].ev.MangaID)
		}
	})
	n.table.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'c':
			n.items = nil
			n.render()
		case 's':
			a.toggleNotify()
		default:
			return ev
		}
		return nil
	})
	n.render()
	return n
}

func (n *NotifyScreen) Name() string           { return "notify" }
func (n *NotifyScreen) Root() tview.Primitive  { return n.root }
func (n *NotifyScreen) Focus() tview.Primitive { return n.table }
func (n *NotifyScreen) Hints() string          { return "c clear · s subscribe/unsubscribe · Enter open" }
func (n *NotifyScreen) OnShow(a *App) {
	n.unread = 0
	a.renderHeader()
	n.render()
}

// add runs on the UI goroutine for every alert; the toast shows on any screen.
func (n *NotifyScreen) add(ev protocol.NewChapter) {
	n.items = append([]notice{{time.Now(), ev}}, n.items...)
	if len(n.items) > maxNotices {
		n.items = n.items[:maxNotices]
	}
	if n.a.current != "notify" {
		n.unread++
	}
	n.render()
	title := ev.Title
	if title == "" {
		title = ev.MangaID
	}
	n.a.Success(fmt.Sprintf("New chapter: %s — ch. %d", title, ev.Chapter))
}

func (n *NotifyScreen) render() {
	c := n.a.conns[1]
	switch {
	case c.OK:
		n.state.SetText(" [green]UDP ● subscribed[-]")
	case c.State == "unsubscribed":
		n.state.SetText(" [gray]UDP ○ not subscribed — press s[-]")
	default:
		n.state.SetText(" [red]UDP ✗ " + tview.Escape(c.State) + "[-]")
	}
	n.table.Clear()
	for col, h := range []string{"Time", "Manga", "Chapter"} {
		n.table.SetCell(0, col, tview.NewTableCell(h).SetSelectable(false).SetAttributes(tcell.AttrBold))
	}
	if len(n.items) == 0 {
		n.table.SetCell(1, 0, tview.NewTableCell("No alerts yet — they arrive when an admin releases a chapter").
			SetTextColor(tcell.ColorGray).SetSelectable(false))
		return
	}
	for i, it := range n.items {
		title := it.ev.Title
		if title == "" {
			title = it.ev.MangaID
		}
		n.table.SetCell(i+1, 0, tview.NewTableCell(it.at.Format("15:04:05")))
		n.table.SetCell(i+1, 1, tview.NewTableCell(tview.Escape(title)).SetExpansion(1).SetMaxWidth(40))
		n.table.SetCell(i+1, 2, tview.NewTableCell(strconv.Itoa(it.ev.Chapter)).SetAlign(tview.AlignRight))
	}
}
