package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

// Event is applied on the UI goroutine by the single dispatcher.
type Event interface{ Apply(a *App) }

// EventFunc lets a closure be an Event.
type EventFunc func(a *App)

func (f EventFunc) Apply(a *App) { f(a) }

// Screen is one page of the app.
type Screen interface {
	Name() string
	Root() tview.Primitive
	Focus() tview.Primitive
	OnShow(a *App) // runs on the UI goroutine when navigated to
	Hints() string // key hints for the footer
}

// ConnState is what the Status screen shows per channel; TCP/UDP/WS fill it in Phases 7–9.
type ConnState struct {
	Label, State string
	OK           bool
}

// App owns every widget. Thread-safety rule:
//   - tview callbacks (key handlers, button/selected/done funcs) and Event.Apply run on
//     the UI goroutine: they may touch widgets, and must NEVER call QueueUpdate* (it
//     waits for the event loop that is running them, so it deadlocks);
//   - every other goroutine (HTTP calls, timers, network readers) only calls Post.
type App struct {
	tv     *tview.Application
	pages  *tview.Pages
	header *tview.TextView // left: app · user · screen
	conn   *tview.TextView // right: connection dots
	toast  *tview.TextView
	hints  *tview.TextView
	log    *slog.Logger
	api    *client.API

	ctx    context.Context
	cancel context.CancelFunc
	events chan Event

	screens map[string]Screen
	current string
	modals  []modal // stack: the top one has focus
	busy    int
	toastID int

	user      protocol.User
	sessionID string // TCP session id for X-Session-ID; empty until Phase 7
	conns     []ConnState
	health    client.Health
	httpOK    bool

	library *LibraryModel
	browser *Browser
	libView *LibraryScreen
	status  *StatusScreen
}

type modal struct {
	name string
	prev tview.Primitive
}

func New(api *client.API, log *slog.Logger) *App {
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{
		tv: tview.NewApplication(), pages: tview.NewPages(), log: log, api: api,
		header: tview.NewTextView().SetDynamicColors(true),
		conn:   tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight),
		toast:  tview.NewTextView().SetDynamicColors(true),
		hints:  tview.NewTextView().SetDynamicColors(true).SetTextColor(tcell.ColorGray),
		ctx:    ctx, cancel: cancel, events: make(chan Event, 256),
		screens: map[string]Screen{}, library: NewLibraryModel(),
		conns: []ConnState{{"TCP sync", "not started", false}, {"UDP alerts", "not started", false}, {"WS chat", "not started", false}},
	}
	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewFlex().AddItem(a.header, 0, 1, false).AddItem(a.conn, 24, 0, false), 1, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.toast, 1, 0, false).
		AddItem(a.hints, 1, 0, false)
	a.tv.SetRoot(root, true).SetInputCapture(a.globalKeys)

	a.browser = newBrowser(a)
	a.libView = newLibraryScreen(a)
	a.status = newStatusScreen(a)
	for _, s := range []Screen{newLoginScreen(a), a.browser, a.libView, a.status} {
		a.screens[s.Name()] = s
		a.pages.AddPage(s.Name(), s.Root(), true, false)
	}
	return a
}

// SetScreen is for tests (tcell.SimulationScreen).
func (a *App) SetScreen(s tcell.Screen) { a.tv.SetScreen(s) }

// QueueUpdate runs f on the UI goroutine and waits for it. Only for goroutines
// that are not tview callbacks (tests reading the screen); never call it from one.
func (a *App) QueueUpdate(f func()) { a.tv.QueueUpdate(f) }

// Run blocks until the user quits. The dispatcher and the health poller stop with ctx.
func (a *App) Run() error {
	a.Goto("login")
	go a.dispatch()
	go a.pollHealth(5 * time.Second)
	defer a.cancel()
	return a.tv.Run()
}

// Quit: cancel first (stops posting and the dispatcher), then stop tview.
func (a *App) Quit() {
	a.cancel()
	a.tv.Stop()
}

// Post may be called from any goroutine. The buffered channel gives back-pressure;
// after shutdown it returns immediately instead of blocking.
func (a *App) Post(ev Event) {
	select {
	case a.events <- ev:
	case <-a.ctx.Done():
	}
}

// dispatch is the only goroutine that hands events to tview.
func (a *App) dispatch() {
	for {
		select {
		case ev := <-a.events:
			a.tv.QueueUpdateDraw(func() { ev.Apply(a) })
		case <-a.ctx.Done():
			return
		}
	}
}

// Go runs work off the UI goroutine and applies its result on it. Call it from the
// UI goroutine only; work must not touch widgets.
func (a *App) Go(work func() func(a *App)) {
	a.setBusy(+1)
	go func() {
		apply := work()
		a.Post(EventFunc(func(a *App) { a.setBusy(-1); apply(a) }))
	}()
}

func (a *App) Goto(name string) {
	s, ok := a.screens[name]
	if !ok {
		a.Info(name + " arrives in a later phase")
		return
	}
	a.current = name
	a.pages.SwitchToPage(name)
	a.tv.SetFocus(s.Focus())
	a.hints.SetText(s.Hints() + "   [::d]1 Browser  2 Library  5/? Status & Help  Ctrl+C Quit")
	a.renderHeader()
	s.OnShow(a)
}

// isTyping: global letter/number keys must reach text fields untouched.
func (a *App) isTyping() bool {
	switch a.tv.GetFocus().(type) {
	case *tview.InputField, *tview.TextArea:
		return true
	}
	return false
}

func (a *App) globalKeys(ev *tcell.EventKey) *tcell.EventKey {
	if ev.Key() == tcell.KeyCtrlC {
		a.confirmQuit()
		return nil
	}
	if len(a.modals) > 0 || a.isTyping() || a.user.ID == "" || ev.Key() != tcell.KeyRune {
		return ev
	}
	if name, ok := globalKeys[ev.Rune()]; ok {
		a.Goto(name)
		return nil
	}
	return ev
}

func (a *App) confirmQuit() {
	if a.hasModal("quit") {
		return
	}
	m := tview.NewModal().SetText("Quit MangaHub?").AddButtons([]string{"Quit", "Cancel"}).
		SetDoneFunc(func(_ int, label string) {
			if label == "Quit" {
				a.Quit()
				return
			}
			a.CloseModal("quit")
		})
	a.pages.AddPage("quit", m, false, true)
	a.modals = append(a.modals, modal{"quit", a.tv.GetFocus()})
	a.tv.SetFocus(m)
}

// OpenModal centres p (w×h cells) above the current screen and focuses it.
func (a *App) OpenModal(name string, p tview.Primitive, w, h int) {
	box := tview.NewFlex().AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).AddItem(p, h, 0, true).AddItem(nil, 0, 1, false), w, 0, true).
		AddItem(nil, 0, 1, false)
	a.modals = append(a.modals, modal{name, a.tv.GetFocus()})
	a.pages.AddPage(name, box, true, true)
	a.tv.SetFocus(p)
}

// CloseModal removes it and restores the focus explicitly (tview does not).
func (a *App) CloseModal(name string) {
	for i := len(a.modals) - 1; i >= 0; i-- {
		if a.modals[i].name == name {
			prev := a.modals[i].prev
			a.modals = append(a.modals[:i], a.modals[i+1:]...)
			a.pages.RemovePage(name)
			a.tv.SetFocus(prev)
			return
		}
	}
}

func (a *App) hasModal(name string) bool {
	for _, m := range a.modals {
		if m.name == name {
			return true
		}
	}
	return false
}

func (a *App) Info(msg string)    { a.showToast("[white]", msg) }
func (a *App) Success(msg string) { a.showToast("[green]", msg) }
func (a *App) Error(msg string)   { a.showToast("[red]", msg) }

// showToast writes one coloured footer line and clears it after 4 s (unless replaced).
func (a *App) showToast(color, msg string) {
	a.toastID++
	id := a.toastID
	a.toast.SetText(" " + color + tview.Escape(msg))
	time.AfterFunc(4*time.Second, func() {
		a.Post(EventFunc(func(a *App) {
			if a.toastID == id {
				a.toast.Clear()
			}
		}))
	})
}

// Fail shows err; a 401 outside login ends the session and returns to Login.
func (a *App) Fail(err error) {
	if client.IsUnauthorized(err) {
		a.Logout()
	}
	a.Error(client.FriendlyMessage(err))
}

func (a *App) Logout() {
	a.api.Clear()
	a.user = protocol.User{}
	a.library.Replace(nil)
	for len(a.modals) > 0 {
		a.CloseModal(a.modals[len(a.modals)-1].name)
	}
	a.Goto("login") // the real-time clients (Phases 7–9) are stopped here
}

func (a *App) loggedIn(lr protocol.LoginResponse) {
	a.user = lr.User
	a.log.Info("logged in", "user", lr.User.Username) // never the token
	a.Goto("browser")
	a.Success("Welcome, " + lr.User.Username)
	// Real-time start hook: TCP/UDP/WS clients start here in Phases 7–9.
}

func (a *App) setBusy(d int) {
	a.busy += d
	a.renderHeader()
}

func (a *App) renderHeader() {
	user := "not logged in"
	if a.user.Username != "" {
		user = tview.Escape(a.user.Username)
	}
	busy := ""
	if a.busy > 0 {
		busy = " [yellow]…[-]"
	}
	a.header.SetText(fmt.Sprintf(" [::b]MangaHub[::-] · %s · %s%s", user, a.current, busy))
	a.conn.SetText(dot("HTTP", a.httpOK) + " " + dot("catalog", a.httpOK && a.health.Catalog == "up") + " ")
}

func dot(label string, ok bool) string {
	if ok {
		return "[green]" + label + " ●[-]"
	}
	return "[red]" + label + " ○[-]"
}

// pollHealth runs on its own goroutine: it only calls the client and Post.
func (a *App) pollHealth(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		ctx, cancel := context.WithTimeout(a.ctx, 2*time.Second)
		h, err := a.api.Health(ctx)
		cancel()
		a.Post(EventFunc(func(a *App) {
			a.httpOK, a.health = err == nil, h
			a.renderHeader()
			a.status.render(a)
		}))
		select {
		case <-t.C:
		case <-a.ctx.Done():
			return
		}
	}
}
