package app

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
)

type StatusScreen struct{ view *tview.TextView }

func newStatusScreen(*App) *StatusScreen {
	s := &StatusScreen{view: tview.NewTextView().SetDynamicColors(true).SetScrollable(true)}
	s.view.SetBorder(true).SetTitle(" Connection Status & Help ")
	return s
}

func (s *StatusScreen) Name() string           { return "status" }
func (s *StatusScreen) Root() tview.Primitive  { return s.view }
func (s *StatusScreen) Focus() tview.Primitive { return s.view }
func (s *StatusScreen) Hints() string          { return "↑/↓ scroll" }
func (s *StatusScreen) OnShow(a *App)          { s.render(a) }

// render runs on the UI goroutine (from OnShow or the health event).
func (s *StatusScreen) render(a *App) {
	var b strings.Builder
	b.WriteString("[::b]Connections[::-]  (checked every 5 s)\n")
	http := "unreachable"
	if a.httpOK {
		http = "ok"
	}
	row := func(label, state string, ok bool) {
		fmt.Fprintf(&b, "  %-12s %s  %s\n", label, dot("", ok), tview.Escape(state))
	}
	row("HTTP", a.api.Base()+" — "+http, a.httpOK)
	row("Catalog", orDash(a.health.Catalog), a.httpOK && a.health.Catalog == "up")
	for _, c := range a.conns {
		row(c.Label, c.State, c.OK)
	}

	b.WriteString("\n[::b]Screens[::-]\n  Login → Browser → Details → Add to Library\n  Library → Update chapter / status\n  Discussion · Notifications · Status & Help\n")
	b.WriteString("\n[::b]Keys[::-]\n")
	for _, k := range keyHelp {
		fmt.Fprintf(&b, "  [gray]%-8s[-] %-8s %s\n", k.Scope, tview.Escape(k.Keys), k.Action)
	}
	s.view.SetText(b.String())
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
