package widgets

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/emersion/go-message/mail"
	"github.com/gdamore/tcell/v2"
	"github.com/google/shlex"

	"git.sr.ht/~rjarry/aerc/config"
	"git.sr.ht/~rjarry/aerc/lib"
	"git.sr.ht/~rjarry/aerc/lib/crypto"
	"git.sr.ht/~rjarry/aerc/lib/ui"
	"git.sr.ht/~rjarry/aerc/log"
	"git.sr.ht/~rjarry/aerc/worker/types"
)

type Aerc struct {
	accounts    map[string]*AccountView
	cmd         func(cmd []string) error
	cmdHistory  lib.History
	complete    func(cmd string) []string
	focused     ui.Interactive
	grid        *ui.Grid
	simulating  int
	statusbar   *ui.Stack
	statusline  *StatusLine
	pasting     bool
	pendingKeys []config.KeyStroke
	prompts     *ui.Stack
	tabs        *ui.Tabs
	ui          *ui.UI
	beep        func() error
	dialog      ui.DrawableInteractive

	Crypto crypto.Provider
}

type Choice struct {
	Key     string
	Text    string
	Command []string
}

func NewAerc(
	crypto crypto.Provider, cmd func(cmd []string) error,
	complete func(cmd string) []string, cmdHistory lib.History,
	deferLoop chan struct{},
) *Aerc {
	tabs := ui.NewTabs(config.Ui)

	statusbar := ui.NewStack(config.Ui)
	statusline := NewStatusLine(config.Ui)
	statusbar.Push(statusline)

	grid := ui.NewGrid().Rows([]ui.GridSpec{
		{Strategy: ui.SIZE_EXACT, Size: ui.Const(1)},
		{Strategy: ui.SIZE_WEIGHT, Size: ui.Const(1)},
		{Strategy: ui.SIZE_EXACT, Size: ui.Const(1)},
	}).Columns([]ui.GridSpec{
		{Strategy: ui.SIZE_WEIGHT, Size: ui.Const(1)},
	})
	grid.AddChild(tabs.TabStrip)
	grid.AddChild(tabs.TabContent).At(1, 0)
	grid.AddChild(statusbar).At(2, 0)

	aerc := &Aerc{
		accounts:   make(map[string]*AccountView),
		cmd:        cmd,
		cmdHistory: cmdHistory,
		complete:   complete,
		grid:       grid,
		statusbar:  statusbar,
		statusline: statusline,
		prompts:    ui.NewStack(config.Ui),
		tabs:       tabs,
		Crypto:     crypto,
	}

	statusline.SetAerc(aerc)
	config.Triggers.ExecuteCommand = cmd

	for _, acct := range config.Accounts {
		view, err := NewAccountView(aerc, acct, aerc, deferLoop)
		if err != nil {
			tabs.Add(errorScreen(err.Error()), acct.Name, nil)
		} else {
			aerc.accounts[acct.Name] = view
			view.tab = tabs.Add(view, acct.Name, view.UiConfig())
		}
	}

	if len(config.Accounts) == 0 {
		wizard := NewAccountWizard(aerc)
		wizard.Focus(true)
		aerc.NewTab(wizard, "New account")
	}

	tabs.Select(0)

	tabs.CloseTab = func(index int) {
		tab := aerc.tabs.Get(index)
		if tab == nil {
			return
		}
		switch content := tab.Content.(type) {
		case *AccountView:
			return
		case *AccountWizard:
			return
		case *Composer:
			aerc.RemoveTab(content)
			content.Close()
		case *Terminal:
			content.Close(nil)
		case *MessageViewer:
			aerc.RemoveTab(content)
		}
	}

	if config.Ui.IndexFormat != "" {
		ini := config.ColumnDefsToIni(
			config.Ui.IndexColumns, "index-columns")
		title := "DEPRECATION WARNING"
		text := `
The index-format setting is deprecated. It has been replaced by index-columns.

Your configuration in this instance was automatically converted to:

[ui]
` + ini + `
Your configuration file was not changed. To make this change permanent and to
dismiss this deprecation warning on launch, copy the above lines into aerc.conf
and remove index-format from it. See aerc-config(5) for more details.

index-format will be removed in aerc 0.17.
`
		aerc.AddDialog(NewSelectorDialog(
			title, text, []string{"OK"}, 0,
			aerc.SelectedAccountUiConfig(),
			func(string, error) { aerc.CloseDialog() },
		))
	}

	return aerc
}

func (aerc *Aerc) OnBeep(f func() error) {
	aerc.beep = f
}

func (aerc *Aerc) Beep() {
	if aerc.beep == nil {
		log.Warnf("should beep, but no beeper")
		return
	}
	if err := aerc.beep(); err != nil {
		log.Errorf("tried to beep, but could not: %v", err)
	}
}

func (aerc *Aerc) HandleMessage(msg types.WorkerMessage) {
	if acct, ok := aerc.accounts[msg.Account()]; ok {
		acct.onMessage(msg)
	}
}

func (aerc *Aerc) Invalidate() {
	ui.Invalidate()
}

func (aerc *Aerc) Focus(focus bool) {
	// who cares
}

func (aerc *Aerc) Draw(ctx *ui.Context) {
	if len(aerc.prompts.Children()) > 0 {
		previous := aerc.focused
		prompt := aerc.prompts.Pop().(*ExLine)
		prompt.finish = func() {
			aerc.statusbar.Pop()
			aerc.focus(previous)
		}

		aerc.statusbar.Push(prompt)
		aerc.focus(prompt)
	}
	aerc.grid.Draw(ctx)
	if aerc.dialog != nil {
		if w, h := ctx.Width(), ctx.Height(); w > 8 && h > 4 {
			if d, ok := aerc.dialog.(Dialog); ok {
				start, height := d.ContextHeight()
				aerc.dialog.Draw(
					ctx.Subcontext(4, start(h),
						w-8, height(h)))
			} else {
				aerc.dialog.Draw(ctx.Subcontext(4, h/2-2, w-8, 4))
			}
		}
	}
}

func (aerc *Aerc) HumanReadableBindings() []string {
	var result []string
	binds := aerc.getBindings()
	format := func(s string) string {
		s = strings.ReplaceAll(s, "<space>", " ")
		return strings.ReplaceAll(s, "%", "%%")
	}
	fmtStr := "%10s %s"
	for _, bind := range binds.Bindings {
		result = append(result, fmt.Sprintf(fmtStr,
			format(config.FormatKeyStrokes(bind.Input)),
			format(config.FormatKeyStrokes(bind.Output)),
		))
	}
	if binds.Globals && config.Binds.Global != nil {
		for _, bind := range config.Binds.Global.Bindings {
			result = append(result, fmt.Sprintf(fmtStr+" (Globals)",
				format(config.FormatKeyStrokes(bind.Input)),
				format(config.FormatKeyStrokes(bind.Output)),
			))
		}
	}
	result = append(result, fmt.Sprintf(fmtStr,
		"$ex",
		fmt.Sprintf("'%c'", binds.ExKey.Rune),
	))
	result = append(result, fmt.Sprintf(fmtStr,
		"Globals",
		fmt.Sprintf("%v", binds.Globals),
	))
	sort.Strings(result)
	return result
}

func (aerc *Aerc) getBindings() *config.KeyBindings {
	selectedAccountName := ""
	if aerc.SelectedAccount() != nil {
		selectedAccountName = aerc.SelectedAccount().acct.Name
	}
	switch view := aerc.SelectedTabContent().(type) {
	case *AccountView:
		binds := config.Binds.MessageList.ForAccount(selectedAccountName)
		return binds.ForFolder(view.SelectedDirectory())
	case *AccountWizard:
		return config.Binds.AccountWizard
	case *Composer:
		switch view.Bindings() {
		case "compose::editor":
			return config.Binds.ComposeEditor.ForAccount(
				selectedAccountName)
		case "compose::review":
			return config.Binds.ComposeReview.ForAccount(
				selectedAccountName)
		default:
			return config.Binds.Compose.ForAccount(
				selectedAccountName)
		}
	case *MessageViewer:
		switch view.Bindings() {
		case "view::passthrough":
			return config.Binds.MessageViewPassthrough.ForAccount(
				selectedAccountName)
		default:
			return config.Binds.MessageView.ForAccount(
				selectedAccountName)
		}
	case *Terminal:
		return config.Binds.Terminal
	default:
		return config.Binds.Global
	}
}

func (aerc *Aerc) simulate(strokes []config.KeyStroke) {
	aerc.pendingKeys = []config.KeyStroke{}
	aerc.simulating += 1
	for _, stroke := range strokes {
		simulated := tcell.NewEventKey(
			stroke.Key, stroke.Rune, tcell.ModNone)
		aerc.Event(simulated)
	}
	aerc.simulating -= 1
	// If we are still focused on the exline, turn on tab complete
	if exline, ok := aerc.focused.(*ExLine); ok {
		exline.TabComplete(func(cmd string) ([]string, string) {
			return aerc.complete(cmd), ""
		})
		// send tab to text input to trigger completion
		exline.Event(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	}
}

func (aerc *Aerc) Event(event tcell.Event) bool {
	if aerc.dialog != nil {
		return aerc.dialog.Event(event)
	}

	if aerc.focused != nil {
		return aerc.focused.Event(event)
	}

	switch event := event.(type) {
	case *tcell.EventKey:
		// If we are in a bracketed paste, don't process the keys for
		// bindings
		if aerc.pasting {
			interactive, ok := aerc.SelectedTabContent().(ui.Interactive)
			if ok {
				return interactive.Event(event)
			}
			return false
		}
		aerc.statusline.Expire()
		aerc.pendingKeys = append(aerc.pendingKeys, config.KeyStroke{
			Modifiers: event.Modifiers(),
			Key:       event.Key(),
			Rune:      event.Rune(),
		})
		ui.Invalidate()
		bindings := aerc.getBindings()
		incomplete := false
		result, strokes := bindings.GetBinding(aerc.pendingKeys)
		switch result {
		case config.BINDING_FOUND:
			aerc.simulate(strokes)
			return true
		case config.BINDING_INCOMPLETE:
			incomplete = true
		case config.BINDING_NOT_FOUND:
		}
		if bindings.Globals {
			result, strokes = config.Binds.Global.GetBinding(aerc.pendingKeys)
			switch result {
			case config.BINDING_FOUND:
				aerc.simulate(strokes)
				return true
			case config.BINDING_INCOMPLETE:
				incomplete = true
			case config.BINDING_NOT_FOUND:
			}
		}
		if !incomplete {
			aerc.pendingKeys = []config.KeyStroke{}
			exKey := bindings.ExKey
			if aerc.simulating > 0 {
				// Keybindings still use : even if you change the ex key
				exKey = config.Binds.Global.ExKey
			}
			if aerc.isExKey(event, exKey) {
				aerc.BeginExCommand("")
				return true
			}
			interactive, ok := aerc.SelectedTabContent().(ui.Interactive)
			if ok {
				return interactive.Event(event)
			}
			return false
		}
	case *tcell.EventMouse:
		x, y := event.Position()
		aerc.grid.MouseEvent(x, y, event)
		return true
	case *tcell.EventPaste:
		if event.Start() {
			aerc.pasting = true
		}
		if event.End() {
			aerc.pasting = false
		}
		interactive, ok := aerc.SelectedTabContent().(ui.Interactive)
		if ok {
			return interactive.Event(event)
		}
		return false
	}
	return false
}

func (aerc *Aerc) SelectedAccount() *AccountView {
	return aerc.account(aerc.SelectedTabContent())
}

func (aerc *Aerc) Account(name string) (*AccountView, error) {
	if acct, ok := aerc.accounts[name]; ok {
		return acct, nil
	}
	return nil, fmt.Errorf("account <%s> not found", name)
}

func (aerc *Aerc) PrevAccount() (*AccountView, error) {
	cur := aerc.SelectedAccount()
	if cur == nil {
		return nil, fmt.Errorf("no account selected, cannot get prev")
	}
	for i, conf := range config.Accounts {
		if conf.Name == cur.Name() {
			i -= 1
			if i == -1 {
				i = len(config.Accounts) - 1
			}
			conf = config.Accounts[i]
			return aerc.Account(conf.Name)
		}
	}
	return nil, fmt.Errorf("no prev account")
}

func (aerc *Aerc) NextAccount() (*AccountView, error) {
	cur := aerc.SelectedAccount()
	if cur == nil {
		return nil, fmt.Errorf("no account selected, cannot get next")
	}
	for i, conf := range config.Accounts {
		if conf.Name == cur.Name() {
			i += 1
			if i == len(config.Accounts) {
				i = 0
			}
			conf = config.Accounts[i]
			return aerc.Account(conf.Name)
		}
	}
	return nil, fmt.Errorf("no next account")
}

func (aerc *Aerc) AccountNames() []string {
	results := make([]string, 0)
	for name := range aerc.accounts {
		results = append(results, name)
	}
	return results
}

func (aerc *Aerc) account(d ui.Drawable) *AccountView {
	switch tab := d.(type) {
	case *AccountView:
		return tab
	case *MessageViewer:
		return tab.SelectedAccount()
	case *Composer:
		return tab.Account()
	}
	return nil
}

func (aerc *Aerc) SelectedAccountUiConfig() *config.UIConfig {
	acct := aerc.SelectedAccount()
	if acct == nil {
		return config.Ui
	}
	return acct.UiConfig()
}

func (aerc *Aerc) SelectedTabContent() ui.Drawable {
	tab := aerc.tabs.Selected()
	if tab == nil {
		return nil
	}
	return tab.Content
}

func (aerc *Aerc) SelectedTab() *ui.Tab {
	return aerc.tabs.Selected()
}

func (aerc *Aerc) NewTab(clickable ui.Drawable, name string) *ui.Tab {
	uiConf := config.Ui
	if acct := aerc.account(clickable); acct != nil {
		uiConf = acct.UiConfig()
	}
	tab := aerc.tabs.Add(clickable, name, uiConf)
	aerc.UpdateStatus()
	return tab
}

func (aerc *Aerc) RemoveTab(tab ui.Drawable) {
	aerc.tabs.Remove(tab)
	aerc.UpdateStatus()
}

func (aerc *Aerc) ReplaceTab(tabSrc ui.Drawable, tabTarget ui.Drawable, name string) {
	aerc.tabs.Replace(tabSrc, tabTarget, name)
}

func (aerc *Aerc) MoveTab(i int, relative bool) {
	aerc.tabs.MoveTab(i, relative)
}

func (aerc *Aerc) PinTab() {
	aerc.tabs.PinTab()
}

func (aerc *Aerc) UnpinTab() {
	aerc.tabs.UnpinTab()
}

func (aerc *Aerc) NextTab() {
	aerc.tabs.NextTab()
}

func (aerc *Aerc) PrevTab() {
	aerc.tabs.PrevTab()
}

func (aerc *Aerc) SelectTab(name string) bool {
	ok := aerc.tabs.SelectName(name)
	if ok {
		aerc.UpdateStatus()
	}
	return ok
}

func (aerc *Aerc) SelectTabIndex(index int) bool {
	ok := aerc.tabs.Select(index)
	if ok {
		aerc.UpdateStatus()
	}
	return ok
}

func (aerc *Aerc) TabNames() []string {
	return aerc.tabs.Names()
}

func (aerc *Aerc) SelectPreviousTab() bool {
	return aerc.tabs.SelectPrevious()
}

func (aerc *Aerc) SetStatus(status string) *StatusMessage {
	return aerc.statusline.Set(status)
}

func (aerc *Aerc) UpdateStatus() {
	if acct := aerc.SelectedAccount(); acct != nil {
		acct.UpdateStatus()
	} else {
		aerc.ClearStatus()
	}
}

func (aerc *Aerc) ClearStatus() {
	aerc.statusline.Set("")
}

func (aerc *Aerc) SetError(status string) *StatusMessage {
	return aerc.statusline.SetError(status)
}

func (aerc *Aerc) PushStatus(text string, expiry time.Duration) *StatusMessage {
	return aerc.statusline.Push(text, expiry)
}

func (aerc *Aerc) PushError(text string) *StatusMessage {
	return aerc.statusline.PushError(text)
}

func (aerc *Aerc) PushWarning(text string) *StatusMessage {
	return aerc.statusline.PushWarning(text)
}

func (aerc *Aerc) PushSuccess(text string) *StatusMessage {
	return aerc.statusline.PushSuccess(text)
}

func (aerc *Aerc) focus(item ui.Interactive) {
	if aerc.focused == item {
		return
	}
	if aerc.focused != nil {
		aerc.focused.Focus(false)
	}
	aerc.focused = item
	interactive, ok := aerc.SelectedTabContent().(ui.Interactive)
	if item != nil {
		item.Focus(true)
		if ok {
			interactive.Focus(false)
		}
	} else if ok {
		interactive.Focus(true)
	}
}

func (aerc *Aerc) BeginExCommand(cmd string) {
	previous := aerc.focused
	var tabComplete func(string) ([]string, string)
	if aerc.simulating != 0 {
		// Don't try to draw completions for simulated events
		tabComplete = nil
	} else {
		tabComplete = func(cmd string) ([]string, string) {
			return aerc.complete(cmd), ""
		}
	}
	exline := NewExLine(cmd, func(cmd string) {
		parts, err := shlex.Split(cmd)
		if err != nil {
			aerc.PushError(err.Error())
		}
		err = aerc.cmd(parts)
		if err != nil {
			aerc.PushError(err.Error())
		}
		// only add to history if this is an unsimulated command,
		// ie one not executed from a keybinding
		if aerc.simulating == 0 {
			aerc.cmdHistory.Add(cmd)
		}
	}, func() {
		aerc.statusbar.Pop()
		aerc.focus(previous)
	}, tabComplete, aerc.cmdHistory)
	aerc.statusbar.Push(exline)
	aerc.focus(exline)
}

func (aerc *Aerc) PushPrompt(prompt *ExLine) {
	aerc.prompts.Push(prompt)
}

func (aerc *Aerc) RegisterPrompt(prompt string, cmd []string) {
	p := NewPrompt(prompt, func(text string) {
		if text != "" {
			cmd = append(cmd, text)
		}
		err := aerc.cmd(cmd)
		if err != nil {
			aerc.PushError(err.Error())
		}
	}, func(cmd string) ([]string, string) {
		return nil, "" // TODO: completions
	})
	aerc.prompts.Push(p)
}

func (aerc *Aerc) RegisterChoices(choices []Choice) {
	cmds := make(map[string][]string)
	texts := []string{}
	for _, c := range choices {
		text := fmt.Sprintf("[%s] %s", c.Key, c.Text)
		if strings.Contains(c.Text, c.Key) {
			text = strings.Replace(c.Text, c.Key, "["+c.Key+"]", 1)
		}
		texts = append(texts, text)
		cmds[c.Key] = c.Command
	}
	prompt := strings.Join(texts, ", ") + "? "
	p := NewPrompt(prompt, func(text string) {
		cmd, ok := cmds[text]
		if !ok {
			return
		}
		err := aerc.cmd(cmd)
		if err != nil {
			aerc.PushError(err.Error())
		}
	}, func(cmd string) ([]string, string) {
		return nil, "" // TODO: completions
	})
	aerc.prompts.Push(p)
}

func (aerc *Aerc) Mailto(addr *url.URL) error {
	var subject string
	var body string
	var acctName string
	var attachments []string
	h := &mail.Header{}
	to, err := mail.ParseAddressList(addr.Opaque)
	if err != nil && addr.Opaque != "" {
		return fmt.Errorf("Could not parse to: %w", err)
	}
	h.SetAddressList("to", to)
	for key, vals := range addr.Query() {
		switch strings.ToLower(key) {
		case "account":
			acctName = strings.Join(vals, "")
		case "bcc":
			list, err := mail.ParseAddressList(strings.Join(vals, ","))
			if err != nil {
				break
			}
			h.SetAddressList("Bcc", list)
		case "body":
			body = strings.Join(vals, "\n")
		case "cc":
			list, err := mail.ParseAddressList(strings.Join(vals, ","))
			if err != nil {
				break
			}
			h.SetAddressList("Cc", list)
		case "in-reply-to":
			for i, msgID := range vals {
				if len(msgID) > 1 && msgID[0] == '<' &&
					msgID[len(msgID)-1] == '>' {
					vals[i] = msgID[1 : len(msgID)-1]
				}
			}
			h.SetMsgIDList("In-Reply-To", vals)
		case "subject":
			subject = strings.Join(vals, ",")
			h.SetText("Subject", subject)
		case "attach":
			for _, path := range vals {
				// remove a potential file:// prefix.
				attachments = append(attachments, strings.TrimPrefix(path, "file://"))
			}
		default:
			// any other header gets ignored on purpose to avoid control headers
			// being injected
		}
	}

	acct := aerc.SelectedAccount()
	if acctName != "" {
		if a, ok := aerc.accounts[acctName]; ok && a != nil {
			acct = a
		}
	}

	if acct == nil {
		return errors.New("No account selected")
	}

	composer, err := NewComposer(aerc, acct,
		acct.AccountConfig(), acct.Worker(), "", h, nil)
	if err != nil {
		return nil
	}
	composer.SetContents(strings.NewReader(body))
	composer.FocusEditor("subject")
	title := "New email"
	if subject != "" {
		title = subject
		composer.FocusTerminal()
	}
	if to == nil {
		composer.FocusEditor("to")
	}
	tab := aerc.NewTab(composer, title)
	composer.OnHeaderChange("Subject", func(subject string) {
		if subject == "" {
			tab.Name = "New email"
		} else {
			tab.Name = subject
		}
		ui.Invalidate()
	})

	for _, file := range attachments {
		composer.AddAttachment(file)
	}
	return nil
}

func (aerc *Aerc) Mbox(source string) error {
	acctConf := config.AccountConfig{}
	if selectedAcct := aerc.SelectedAccount(); selectedAcct != nil {
		acctConf = *selectedAcct.acct
		info := fmt.Sprintf("Loading outgoing mbox mail settings from account [%s]", selectedAcct.Name())
		aerc.PushStatus(info, 10*time.Second)
		log.Debugf(info)
	} else {
		acctConf.From = &mail.Address{Address: "user@localhost"}
	}
	acctConf.Name = "mbox"
	acctConf.Source = source
	acctConf.Default = "INBOX"
	acctConf.Archive = "Archive"
	acctConf.Postpone = "Drafts"
	acctConf.CopyTo = "Sent"

	mboxView, err := NewAccountView(aerc, &acctConf, aerc, nil)
	if err != nil {
		aerc.NewTab(errorScreen(err.Error()), acctConf.Name)
	} else {
		aerc.accounts[acctConf.Name] = mboxView
		aerc.NewTab(mboxView, acctConf.Name)
	}
	return nil
}

func (aerc *Aerc) CloseBackends() error {
	var returnErr error
	for _, acct := range aerc.accounts {
		var raw interface{} = acct.worker.Backend
		c, ok := raw.(io.Closer)
		if !ok {
			continue
		}
		err := c.Close()
		if err != nil {
			returnErr = err
			log.Errorf("Closing backend failed for %s: %v", acct.Name(), err)
		}
	}
	return returnErr
}

func (aerc *Aerc) AddDialog(d ui.DrawableInteractive) {
	aerc.dialog = d
	aerc.Invalidate()
}

func (aerc *Aerc) CloseDialog() {
	aerc.dialog = nil
	aerc.Invalidate()
}

func (aerc *Aerc) GetPassword(title string, prompt string) (chText chan string, chErr chan error) {
	chText = make(chan string, 1)
	chErr = make(chan error, 1)
	getPasswd := NewGetPasswd(title, prompt, func(pw string, err error) {
		defer func() {
			close(chErr)
			close(chText)
			aerc.CloseDialog()
		}()
		if err != nil {
			chErr <- err
			return
		}
		chErr <- nil
		chText <- pw
	})
	aerc.AddDialog(getPasswd)

	return
}

func (aerc *Aerc) Initialize(ui *ui.UI) {
	aerc.ui = ui
}

func (aerc *Aerc) DecryptKeys(keys []openpgp.Key, symmetric bool) (b []byte, err error) {
	for _, key := range keys {
		ident := key.Entity.PrimaryIdentity()
		chPass, chErr := aerc.GetPassword("Decrypt PGP private key",
			fmt.Sprintf("Enter password for %s (%8X)\nPress <ESC> to cancel",
				ident.Name, key.PublicKey.KeyId))

		for err := range chErr {
			if err != nil {
				return nil, err
			}
			pass := <-chPass
			err = key.PrivateKey.Decrypt([]byte(pass))
			return nil, err
		}
	}
	return nil, err
}

// errorScreen is a widget that draws an error in the middle of the context
func errorScreen(s string) ui.Drawable {
	errstyle := config.Ui.GetStyle(config.STYLE_ERROR)
	text := ui.NewText(s, errstyle).Strategy(ui.TEXT_CENTER)
	grid := ui.NewGrid().Rows([]ui.GridSpec{
		{Strategy: ui.SIZE_WEIGHT, Size: ui.Const(1)},
		{Strategy: ui.SIZE_EXACT, Size: ui.Const(1)},
		{Strategy: ui.SIZE_WEIGHT, Size: ui.Const(1)},
	}).Columns([]ui.GridSpec{
		{Strategy: ui.SIZE_WEIGHT, Size: ui.Const(1)},
	})
	grid.AddChild(ui.NewFill(' ', tcell.StyleDefault)).At(0, 0)
	grid.AddChild(text).At(1, 0)
	grid.AddChild(ui.NewFill(' ', tcell.StyleDefault)).At(2, 0)
	return grid
}

func (aerc *Aerc) isExKey(event *tcell.EventKey, exKey config.KeyStroke) bool {
	if event.Key() == tcell.KeyRune {
		// Compare runes if it's a KeyRune
		return event.Modifiers() == exKey.Modifiers && event.Rune() == exKey.Rune
	}
	return event.Modifiers() == exKey.Modifiers && event.Key() == exKey.Key
}

// CmdFallbackSearch checks cmds for the first executable availabe in PATH. An error is
// returned if none are found
func (aerc *Aerc) CmdFallbackSearch(cmds []string) (string, error) {
	var tried []string
	for _, cmd := range cmds {
		if cmd == "" {
			continue
		}
		params := strings.Split(cmd, " ")
		_, err := exec.LookPath(params[0])
		if err != nil {
			tried = append(tried, cmd)
			warn := fmt.Sprintf("cmd '%s' not found in PATH, using fallback", cmd)
			aerc.PushWarning(warn)
			continue
		}
		return cmd, nil
	}
	return "", fmt.Errorf("no command found in PATH: %s", tried)
}
