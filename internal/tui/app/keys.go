package app

// globalKeys work everywhere except in text fields and modals. Avoid Ctrl+B
// (tmux), Ctrl+S/Q/Z and bare F-keys. '3' and '4' arrive with Phases 9 and 8.
var globalKeys = map[rune]string{'1': "browser", '2': "library", '3': "chat", '4': "notify", '5': "status", '?': "status"}

// keyHelp is the single table the footer hints and the Help screen read from.
var keyHelp = []struct{ Scope, Keys, Action string }{
	{"Global", "1 / 2", "Manga Browser / My Library"},
	{"Global", "3 / 4", "Discussion / Notifications (later phases)"},
	{"Global", "5 or ?", "Connection Status & Help"},
	{"Global", "Esc", "leave a field or close a dialog"},
	{"Global", "Ctrl+C", "quit (asks first)"},
	{"Browser", "/", "focus the search field; Enter runs the search"},
	{"Browser", "Tab", "switch between search, results and details"},
	{"Browser", "Enter", "reload the selected manga's details"},
	{"Browser", "a", "add the selected manga to your library"},
	{"Browser", "n / p", "next / previous page"},
	{"Library", "u", "update the current chapter"},
	{"Library", "s", "change the reading status"},
	{"Library", "r", "refresh"},
	{"Library", "Enter", "open the manga in the Browser"},
}
