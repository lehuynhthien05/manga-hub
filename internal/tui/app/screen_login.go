package app

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

type LoginScreen struct {
	a    *App
	root *tview.Flex
	form *tview.Form
	msg  *tview.TextView
}

func newLoginScreen(a *App) *LoginScreen {
	s := &LoginScreen{a: a, form: tview.NewForm(), msg: tview.NewTextView().SetDynamicColors(true)}
	s.form.SetBorder(true)
	inner := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).AddItem(s.form, 11, 0, true).AddItem(s.msg, 2, 0, false).AddItem(nil, 0, 1, false)
	s.root = tview.NewFlex().AddItem(nil, 0, 1, false).AddItem(inner, 50, 0, true).AddItem(nil, 0, 1, false)
	s.showLogin("")
	return s
}

func (s *LoginScreen) Name() string           { return "login" }
func (s *LoginScreen) Root() tview.Primitive  { return s.root }
func (s *LoginScreen) Focus() tview.Primitive { return s.form }
func (s *LoginScreen) Hints() string          { return "Tab next field · Enter submit" }
func (s *LoginScreen) OnShow(*App)            {}

func (s *LoginScreen) field(label string) string {
	return s.form.GetFormItemByLabel(label).(*tview.InputField).GetText()
}

// submitOnEnter makes Enter in the last field submit the form.
func (s *LoginScreen) submitOnEnter(label string, submit func()) {
	s.form.GetFormItemByLabel(label).(*tview.InputField).SetDoneFunc(func(k tcell.Key) {
		if k == tcell.KeyEnter {
			submit()
		}
	})
}

func (s *LoginScreen) setMsg(color, text string) { s.msg.SetText(color + tview.Escape(text)) }

func (s *LoginScreen) showLogin(username string) {
	s.form.Clear(true).SetTitle(" MangaHub — Login ")
	s.form.AddInputField("Username", username, 32, nil, nil).
		AddPasswordField("Password", "", 32, '*', nil).
		AddButton("Login", s.login).
		AddButton("Register…", func() { s.setMsg("", ""); s.showRegister() }).
		AddButton("Quit", s.a.confirmQuit)
	s.submitOnEnter("Password", s.login)
	s.form.SetFocus(0)
	s.a.tv.SetFocus(s.form)
}

func (s *LoginScreen) showRegister() {
	s.form.Clear(true).SetTitle(" MangaHub — Create account ")
	s.form.AddInputField("Username", "", 32, nil, nil).
		AddPasswordField("Password", "", 32, '*', nil).
		AddPasswordField("Confirm password", "", 32, '*', nil).
		AddButton("Create account", s.register).
		AddButton("Back", func() { s.setMsg("", ""); s.showLogin("") })
	s.submitOnEnter("Confirm password", s.register)
	s.form.SetFocus(0)
	s.a.tv.SetFocus(s.form)
}

// The same validators as the server, so an invalid form never sends a request.
func (s *LoginScreen) login() {
	user, pw := s.field("Username"), s.field("Password")
	if user == "" || pw == "" {
		s.setMsg("[red]", "Enter a username and a password")
		return
	}
	s.setMsg("[yellow]", "Logging in…")
	s.a.Go(func() func(*App) {
		lr, err := s.a.api.Login(s.a.ctx, user, pw)
		return func(a *App) {
			if err != nil {
				s.setMsg("[red]", client.FriendlyMessage(err)+" — Enter to retry")
				return
			}
			s.setMsg("", "")
			s.showLogin("")
			a.loggedIn(lr)
		}
	})
}

func (s *LoginScreen) register() {
	user, pw, confirm := s.field("Username"), s.field("Password"), s.field("Confirm password")
	for _, err := range []error{protocol.ValidateUsername(user), protocol.ValidatePassword(pw)} {
		if err != nil {
			s.setMsg("[red]", err.Error())
			return
		}
	}
	if pw != confirm {
		s.setMsg("[red]", "The passwords do not match")
		return
	}
	s.setMsg("[yellow]", "Creating account…")
	s.a.Go(func() func(*App) {
		_, err := s.a.api.Register(s.a.ctx, user, pw)
		return func(a *App) {
			if err != nil {
				s.setMsg("[red]", client.FriendlyMessage(err))
				return
			}
			s.showLogin(user)
			s.form.SetFocus(1) // straight to the password
			a.tv.SetFocus(s.form)
			s.setMsg("[green]", "Account created — please log in")
		}
	})
}
