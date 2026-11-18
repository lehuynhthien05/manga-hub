package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

var defaultRooms = []string{"general", "shounen", "seinen"}

// ChatScreen is the Discussion screen. Every method runs on the UI goroutine.
type ChatScreen struct {
	a      *App
	root   *tview.Flex
	list   *tview.List
	views  *tview.Pages
	input  *tview.InputField
	room   string                     // the room in view
	joined map[string]*tview.TextView // one view per joined room
	unread map[string]int
	rooms  []string // list order
	chat   *client.ChatClient
}

func newChatScreen(a *App) *ChatScreen {
	s := &ChatScreen{a: a, list: tview.NewList().ShowSecondaryText(false), views: tview.NewPages(),
		input: tview.NewInputField().SetLabel("> "), joined: map[string]*tview.TextView{}, unread: map[string]int{},
		rooms: append([]string{}, defaultRooms...)}
	s.list.SetBorder(true).SetTitle(" Rooms ")
	s.views.SetBorder(true)
	right := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(s.views, 0, 1, false).AddItem(s.input, 1, 0, true)
	s.root = tview.NewFlex().AddItem(s.list, 22, 0, true).AddItem(right, 0, 1, false)

	s.list.SetSelectedFunc(func(i int, _, _ string, _ rune) {
		if i == len(s.rooms) {
			s.input.SetText("/join ")
			a.tv.SetFocus(s.input)
			return
		}
		s.join(s.rooms[i])
	})
	s.list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyTab {
			a.tv.SetFocus(s.input)
			return nil
		}
		return ev
	})
	s.input.SetDoneFunc(func(k tcell.Key) {
		switch k {
		case tcell.KeyEnter:
			s.submit(strings.TrimSpace(s.input.GetText()))
		case tcell.KeyEscape, tcell.KeyTab:
			a.tv.SetFocus(s.list)
		}
	})
	s.renderList()
	return s
}

func (s *ChatScreen) Name() string           { return "chat" }
func (s *ChatScreen) Root() tview.Primitive  { return s.root }
func (s *ChatScreen) Focus() tview.Primitive { return s.list }
func (s *ChatScreen) Hints() string {
	return "Enter join · Tab input · /join /leave /help · Esc rooms"
}
func (s *ChatScreen) OnShow(*App) { s.renderList() }

// clean makes user text inert: control characters (ANSI escapes) go, tview tags are escaped.
func clean(str string) string {
	return tview.Escape(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, str))
}

func (s *ChatScreen) renderList() {
	cur := s.list.GetCurrentItem()
	s.list.Clear()
	for _, r := range s.rooms {
		mark := "  "
		if s.joined[r] != nil {
			mark = "● "
		}
		label := mark + r
		if n := s.unread[r]; n > 0 {
			label += fmt.Sprintf(" (%d)", n)
		}
		s.list.AddItem(label, "", 0, nil)
	}
	s.list.AddItem("+ custom room…", "", 0, nil)
	s.list.SetCurrentItem(cur)
}

func (s *ChatScreen) view(room string) *tview.TextView {
	v := s.joined[room]
	if v == nil {
		v = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWordWrap(true)
		s.joined[room] = v
		s.views.AddPage(room, v, true, false)
		if !contains(s.rooms, room) {
			s.rooms = append(s.rooms, room)
		}
	}
	return v
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (s *ChatScreen) show(room string) {
	s.room = room
	s.unread[room] = 0
	s.views.SwitchToPage(room)
	s.views.SetTitle(" #" + room + " ")
	s.renderList()
}

func (s *ChatScreen) join(room string) {
	if !protocol.ValidRoom(room) {
		s.a.Error("Invalid room name: use 1-32 of a-z 0-9 _ -")
		return
	}
	if s.chat == nil || !s.chat.Join(room) {
		s.a.Error("Not connected — try again when WS ● is green")
		return
	}
	s.view(room)
	s.show(room)
	s.a.tv.SetFocus(s.input)
}

func (s *ChatScreen) submit(text string) {
	if text == "" {
		return
	}
	s.input.SetText("")
	switch {
	case strings.HasPrefix(text, "/join"):
		s.join(strings.TrimSpace(strings.TrimPrefix(text, "/join")))
	case text == "/leave":
		if s.room == "" || s.joined[s.room] == nil {
			s.a.Info("You are not in a room")
			return
		}
		s.chat.Leave(s.room)
		s.views.RemovePage(s.room)
		delete(s.joined, s.room)
		s.room = ""
		s.views.SetTitle("")
		s.renderList()
		s.a.tv.SetFocus(s.list)
	case text == "/help":
		s.line(s.room, "[gray]/join <room> · /leave · /help · Esc back to rooms · rooms: a-z 0-9 _ -[-]")
	case strings.HasPrefix(text, "/"):
		s.a.Error("Unknown command — /help lists them")
	case s.room == "":
		s.a.Info("Join a room first (Enter on a room)")
	case s.chat == nil || !s.chat.Send(protocol.WSChat{Type: protocol.TypeChat, Room: s.room, Text: text}):
		s.a.Error("Not connected — message not sent")
	}
}

func (s *ChatScreen) line(room, text string) {
	if v := s.joined[room]; v != nil {
		fmt.Fprintln(v, text)
		v.ScrollToEnd()
	}
}

func hhmm(ts int64) string { return time.Unix(ts, 0).Format("15:04") }

// onFrame decodes one server frame (posted by the WS client's reader).
func (s *ChatScreen) onFrame(raw []byte) {
	typ, err := protocol.PeekType(raw)
	if err != nil {
		return
	}
	switch typ {
	case protocol.TypeHistory:
		var h protocol.WSHistory
		if json.Unmarshal(raw, &h) != nil || s.joined[h.Room] == nil {
			return
		}
		v := s.joined[h.Room]
		v.Clear() // a rejoin sends the history again
		for _, m := range h.Messages {
			s.line(h.Room, s.format(m.User, m.Text, m.TS))
		}
	case protocol.TypeChat:
		var m protocol.WSChat
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		s.line(m.Room, s.format(m.User, m.Text, m.TS))
		s.bump(m.Room)
	case protocol.TypeSystem:
		var m protocol.WSSystem
		if json.Unmarshal(raw, &m) == nil {
			s.line(m.Room, "[gray]· "+clean(m.Text)+"[-]")
		}
	case protocol.TypeError:
		var m protocol.WSError
		if json.Unmarshal(raw, &m) == nil {
			s.a.Error("Chat: " + m.Error)
		}
	}
}

func (s *ChatScreen) format(user, text string, ts int64) string {
	name := clean(user)
	if user == s.a.user.Username {
		name = "[green]" + name + "[-]"
	}
	return fmt.Sprintf("[gray][%s][-] %s: %s", hhmm(ts), name, clean(text))
}

func (s *ChatScreen) bump(room string) {
	if room != s.room || s.a.current != "chat" {
		s.unread[room]++
		s.renderList()
	}
}

// reconnected notes the reconnect in every open room (the client re-joins by itself).
func (s *ChatScreen) reconnected() {
	for room := range s.joined {
		s.line(room, "[gray]· reconnected[-]")
	}
}

// reset forgets every room (logout).
func (s *ChatScreen) reset() {
	for room := range s.joined {
		s.views.RemovePage(room)
	}
	s.joined, s.unread, s.room, s.chat = map[string]*tview.TextView{}, map[string]int{}, "", nil
	s.rooms = append([]string{}, defaultRooms...)
	s.views.SetTitle("")
	s.renderList()
}
