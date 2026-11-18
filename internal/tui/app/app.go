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
	addrs  Addrs

	ctx    context.Context
	cancel context.CancelFunc
	events chan Event

	screens map[string]Screen
	current string
	modals  []modal // stack: the top one has focus
	busy    int
	toastID int

	user      protocol.User
	sessionID string             // TCP session id for X-Session-ID; empty while disconnected
	stopSync  context.CancelFunc // stops the TCP client of the current login
	syncGen   int                // drops callbacks from the client of an earlier login
	token     string             // JWT of this login, in memory only (UDP subscribe needs it)
	stopUDP   context.CancelFunc // stops the UDP alert client (sends unsubscribe)
	stopWS    context.CancelFunc // stops the WebSocket chat client (sends a close frame)
	conns     []ConnState
	health    client.Health
	httpOK    bool

	library *LibraryModel
	browser *Browser
	libView *LibraryScreen
	status  *StatusScreen
	notify  *NotifyScreen
	chat    *ChatScreen
}

type modal struct {
	name string
	prev tview.Primitive
}

// Addrs are the real-time endpoints; an empty one disables that client (tests).
type Addrs struct{ TCP, UDP, WS string }

func New(api *client.API, addrs Addrs, log *slog.Logger) *App {
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{
		tv: tview.NewApplication(), pages: tview.NewPages(), log: log, api: api, addrs: addrs,
		header: tview.NewTextView().SetDynamicColors(true),
		conn:   tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight),
		toast:  tview.NewTextView().SetDynamicColors(true),
		hints:  tview.NewTextView().SetDynamicColors(true).SetTextColor(tcell.ColorGray),
		ctx:    ctx, cancel: cancel, events: make(chan Event, 256),
		screens: map[string]Screen{}, library: NewLibraryModel(),
		conns: []ConnState{{"TCP sync", "not started", false}, {"UDP alerts", "not started", false}, {"WS chat", "not started", false}},
	}
	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewFlex().AddItem(a.header, 0, 1, false).AddItem(a.conn, 37, 0, false), 1, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.toast, 1, 0, false).
		AddItem(a.hints, 1, 0, false)
	a.tv.SetRoot(root, true).SetInputCapture(a.globalKeys)

	a.browser = newBrowser(a)
	a.libView = newLibraryScreen(a)
	a.status = newStatusScreen(a)
	a.notify = newNotifyScreen(a)
	a.chat = newChatScreen(a)
	for _, s := range []Screen{newLoginScreen(a), a.browser, a.libView, a.status, a.notify, a.chat} {
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
			// QueueUpdateDraw waits for a full redraw, so take everything already queued
			// (in order) and draw once: a burst of events then costs one frame, not one each.
			batch := []Event{ev}
		drain:
			for len(batch) < cap(a.events) {
				select {
				case ev := <-a.events:
					batch = append(batch, ev)
				default:
					break drain
				}
			}
			a.tv.QueueUpdateDraw(func() {
				for _, ev := range batch {
					ev.Apply(a)
				}
			})
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
	a.hints.SetText(s.Hints() + "   [::d]1-5 screens · ? help")
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
	a.stopRealtime()
	a.api.Clear()
	a.user = protocol.User{}
	a.library.Replace(nil)
	for len(a.modals) > 0 {
		a.CloseModal(a.modals[len(a.modals)-1].name)
	}
	a.Goto("login")
}

func (a *App) loggedIn(lr protocol.LoginResponse) {
	a.user = lr.User
	a.log.Info("logged in", "user", lr.User.Username) // never the token
	a.Goto("browser")
	a.Success("Welcome, " + lr.User.Username)
	a.token = lr.Token
	a.startSync(lr.Token)
	a.startNotify()
	a.startChat()
}

// startSync runs the TCP progress-sync client for this login. Its callbacks run on
// the client's goroutine, so they only Post; the UI goroutine applies the change.
func (a *App) startSync(token string) {
	if a.addrs.TCP == "" {
		return
	}
	a.syncGen++
	gen := a.syncGen
	ctx, cancel := context.WithCancel(a.ctx)
	a.stopSync = cancel
	c := &client.SyncClient{Addr: a.addrs.TCP, Token: token,
		OnState: func(state, sessionID string, err error) {
			a.Post(EventFunc(func(a *App) {
				if gen != a.syncGen {
					return
				}
				a.sessionID = sessionID
				a.conns[0].OK, a.conns[0].State = state == client.StateConnected, state
				if sessionID != "" {
					a.conns[0].State += " · " + sessionID
				}
				if err != nil {
					a.conns[0].State += " · " + err.Error()
					a.log.Info("tcp sync", "state", state, "err", err)
				}
				a.renderHeader()
				a.status.render(a)
			}))
		},
		OnEvent: func(ev protocol.ProgressUpdated) {
			a.Post(EventFunc(func(a *App) {
				if gen == a.syncGen {
					a.onProgress(ev)
				}
			}))
		}}
	go c.Run(ctx)
}

// startNotify runs the UDP alert client; like startSync, its callbacks only Post.
func (a *App) startNotify() {
	if a.addrs.UDP == "" || a.stopUDP != nil {
		return
	}
	gen := a.syncGen
	ctx, cancel := context.WithCancel(a.ctx)
	a.stopUDP = cancel
	c := &client.NotifyClient{Addr: a.addrs.UDP, Token: a.token,
		OnState: func(state string, err error) {
			a.Post(EventFunc(func(a *App) {
				if gen != a.syncGen || a.stopUDP == nil {
					return
				}
				a.conns[1].OK, a.conns[1].State = state == client.UDPSubscribed, state
				if err != nil {
					a.log.Info("udp alerts", "state", state, "err", err)
				}
				a.renderHeader()
				a.notify.render()
				a.status.render(a)
			}))
		},
		OnEvent: func(ev protocol.NewChapter) {
			a.Post(EventFunc(func(a *App) {
				if gen == a.syncGen {
					a.notify.add(ev)
					a.renderHeader()
				}
			}))
		}}
	go c.Run(ctx)
}

func (a *App) stopNotify(state string) {
	if a.stopUDP != nil {
		a.stopUDP() // the client sends a best-effort unsubscribe
		a.stopUDP = nil
	}
	a.conns[1] = ConnState{"UDP alerts", state, false}
}

// toggleNotify is the Notifications screen's "s" key.
func (a *App) toggleNotify() {
	if a.stopUDP != nil {
		a.stopNotify("unsubscribed")
		a.Info("Unsubscribed from chapter alerts")
	} else {
		a.startNotify()
	}
	a.renderHeader()
	a.notify.render()
}

// startChat runs the WebSocket chat client; its callbacks only Post.
func (a *App) startChat() {
	if a.addrs.WS == "" {
		return
	}
	gen := a.syncGen
	ctx, cancel := context.WithCancel(a.ctx)
	a.stopWS = cancel
	wasUp := false
	c := &client.ChatClient{URL: a.addrs.WS, Token: a.token,
		OnState: func(state string, err error) {
			a.Post(EventFunc(func(a *App) {
				if gen != a.syncGen {
					return
				}
				a.conns[2].OK, a.conns[2].State = state == client.StateConnected, state
				if err != nil {
					a.conns[2].State += " · " + err.Error()
					a.log.Info("ws chat", "state", state, "err", err)
				}
				if state == client.StateConnected && wasUp {
					a.chat.reconnected()
				}
				wasUp = wasUp || state == client.StateConnected
				a.renderHeader()
				a.status.render(a)
			}))
		},
		OnFrame: func(raw []byte) {
			a.Post(EventFunc(func(a *App) {
				if gen == a.syncGen {
					a.chat.onFrame(raw)
				}
			}))
		}}
	a.chat.chat = c
	go c.Run(ctx)
}

func (a *App) stopRealtime() {
	if a.stopWS != nil {
		a.stopWS()
		a.stopWS = nil
	}
	a.chat.reset()
	a.conns[2] = ConnState{"WS chat", "not started", false}
	a.stopNotify("not started")
	a.token = ""
	if a.stopSync != nil {
		a.stopSync()
		a.stopSync = nil
	}
	a.syncGen++
	a.sessionID = ""
	a.conns[0] = ConnState{"TCP sync", "not started", false}
}

// onProgress applies a progress_updated pushed by another session of this user.
// Stale events and echoes (version <= known) and unknown manga change nothing;
// the Library reloads from HTTP on every visit anyway.
func (a *App) onProgress(ev protocol.ProgressUpdated) {
	if !a.library.ApplyProgress(ev) {
		return
	}
	if a.current == "library" {
		a.libView.render()
	}
	it, _ := a.library.Get(ev.MangaID)
	title := it.MangaID
	if it.Manga != nil {
		title = it.Manga.Title
	}
	a.conns[0].State = fmt.Sprintf("connected · %s · last event %s", a.sessionID, time.Now().Format("15:04:05"))
	a.Info(fmt.Sprintf("Synced from another session: %s → ch. %d", title, ev.Chapter))
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
	if a.notify != nil && a.notify.unread > 0 {
		busy += fmt.Sprintf(" · [yellow]4:%d new[-]", a.notify.unread) // short: the header must fit 80 columns
	}
	if a.busy > 0 {
		busy += " [yellow]…[-]"
	}
	a.header.SetText(fmt.Sprintf(" [::b]MangaHub[::-] · %s · %s%s", user, a.current, busy))
	a.conn.SetText(dot("HTTP", a.httpOK) + " " + dot("catalog", a.httpOK && a.health.Catalog == "up") + " " +
		dot("TCP", a.conns[0].OK) + " " + dot("UDP", a.conns[1].OK) + " " + dot("WS", a.conns[2].OK) + " ")
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
