package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"

	"git.sr.ht/~sircmpwn/getopt"
	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-isatty"
	"github.com/xo/terminfo"

	"git.sr.ht/~rjarry/aerc/commands"
	"git.sr.ht/~rjarry/aerc/commands/account"
	"git.sr.ht/~rjarry/aerc/commands/compose"
	"git.sr.ht/~rjarry/aerc/commands/msg"
	"git.sr.ht/~rjarry/aerc/commands/msgview"
	"git.sr.ht/~rjarry/aerc/commands/terminal"
	"git.sr.ht/~rjarry/aerc/config"
	"git.sr.ht/~rjarry/aerc/lib/crypto"
	"git.sr.ht/~rjarry/aerc/lib/ipc"
	"git.sr.ht/~rjarry/aerc/lib/templates"
	libui "git.sr.ht/~rjarry/aerc/lib/ui"
	"git.sr.ht/~rjarry/aerc/log"
	"git.sr.ht/~rjarry/aerc/models"
	"git.sr.ht/~rjarry/aerc/widgets"
	"git.sr.ht/~rjarry/aerc/worker/types"
)

func getCommands(selected libui.Drawable) []*commands.Commands {
	switch selected.(type) {
	case *widgets.AccountView:
		return []*commands.Commands{
			account.AccountCommands,
			msg.MessageCommands,
			commands.GlobalCommands,
		}
	case *widgets.Composer:
		return []*commands.Commands{
			compose.ComposeCommands,
			commands.GlobalCommands,
		}
	case *widgets.MessageViewer:
		return []*commands.Commands{
			msgview.MessageViewCommands,
			msg.MessageCommands,
			commands.GlobalCommands,
		}
	case *widgets.Terminal:
		return []*commands.Commands{
			terminal.TerminalCommands,
			commands.GlobalCommands,
		}
	default:
		return []*commands.Commands{commands.GlobalCommands}
	}
}

func execCommand(
	aerc *widgets.Aerc, ui *libui.UI, cmd []string,
	acct *config.AccountConfig, msg *models.MessageInfo,
) error {
	cmds := getCommands(aerc.SelectedTabContent())
	for i, set := range cmds {
		err := set.ExecuteCommand(aerc, cmd, acct, msg)
		if err != nil {
			if errors.As(err, new(commands.NoSuchCommand)) {
				if i == len(cmds)-1 {
					return err
				}
				continue
			}
			if errors.As(err, new(commands.ErrorExit)) {
				ui.Exit()
				return nil
			}
			return err
		}
		break
	}
	return nil
}

func getCompletions(aerc *widgets.Aerc, cmd string) []string {
	var completions []string
	for _, set := range getCommands(aerc.SelectedTabContent()) {
		completions = append(completions, set.GetCompletions(aerc, cmd)...)
	}
	sort.Strings(completions)
	return completions
}

// set at build time
var (
	Version string
	Flags   string
)

func buildInfo() string {
	info := Version
	flags, _ := base64.StdEncoding.DecodeString(Flags)
	if strings.Contains(string(flags), "notmuch") {
		info += " +notmuch"
	}
	info += fmt.Sprintf(" (%s %s %s)",
		runtime.Version(), runtime.GOARCH, runtime.GOOS)
	return info
}

func usage(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	fmt.Fprintln(os.Stderr, "usage: aerc [-v] [-a <account-name[,account-name>] [mailto:...]")
	os.Exit(1)
}

func setWindowTitle() {
	log.Tracef("Parsing terminfo")
	ti, err := terminfo.LoadFromEnv()
	if err != nil {
		log.Warnf("Cannot get terminfo: %v", err)
		return
	}

	if !ti.Has(terminfo.HasStatusLine) {
		log.Infof("Terminal does not have status line support")
		return
	}

	log.Debugf("Setting terminal title")
	buf := new(bytes.Buffer)
	ti.Fprintf(buf, terminfo.ToStatusLine)
	fmt.Fprint(buf, "aerc")
	ti.Fprintf(buf, terminfo.FromStatusLine)
	os.Stderr.Write(buf.Bytes())
}

func main() {
	defer log.PanicHandler()
	opts, optind, err := getopt.Getopts(os.Args, "va:")
	if err != nil {
		usage("error: " + err.Error())
		return
	}
	log.BuildInfo = buildInfo()
	var accts []string
	for _, opt := range opts {
		if opt.Option == 'v' {
			fmt.Println("aerc " + log.BuildInfo)
			return
		}
		if opt.Option == 'a' {
			accts = strings.Split(opt.Value, ",")
		}
	}
	retryExec := false
	args := os.Args[optind:]
	if len(args) > 0 {
		err := ipc.ConnectAndExec(args)
		if err == nil {
			return // other aerc instance takes over
		}
		fmt.Fprintf(os.Stderr, "Failed to communicate to aerc: %v\n", err)
		// continue with setting up a new aerc instance and retry after init
		retryExec = true
	}

	err = config.LoadConfigFromFile(nil, accts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1) //nolint:gocritic // PanicHandler does not need to run as it's not a panic
	}

	log.Infof("Starting up version %s", log.BuildInfo)

	var (
		aerc *widgets.Aerc
		ui   *libui.UI
	)

	deferLoop := make(chan struct{})

	c := crypto.New()
	err = c.Init()
	if err != nil {
		log.Warnf("failed to initialise crypto interface: %v", err)
	}
	defer c.Close()

	aerc = widgets.NewAerc(c, func(
		cmd []string, acct *config.AccountConfig,
		msg *models.MessageInfo,
	) error {
		return execCommand(aerc, ui, cmd, acct, msg)
	}, func(cmd string) []string {
		return getCompletions(aerc, cmd)
	}, &commands.CmdHistory, deferLoop)

	ui, err = libui.Initialize(aerc)
	if err != nil {
		panic(err)
	}
	defer ui.Close()
	log.UICleanup = func() {
		ui.Close()
	}
	close(deferLoop)

	if config.Ui.MouseEnabled {
		ui.EnableMouse()
	}

	as, err := ipc.StartServer(aerc)
	if err != nil {
		log.Warnf("Failed to start Unix server: %v", err)
	} else {
		defer as.Close()
	}

	// set the aerc version so that we can use it in the template funcs
	templates.SetVersion(Version)

	if retryExec {
		// retry execution
		err := ipc.ConnectAndExec(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to communicate to aerc: %v\n", err)
			err = aerc.CloseBackends()
			if err != nil {
				log.Warnf("failed to close backends: %v", err)
			}
			return
		}
	}

	if isatty.IsTerminal(os.Stderr.Fd()) {
		setWindowTitle()
	}

	ui.ChannelEvents()
	for event := range libui.MsgChannel {
		switch event := event.(type) {
		case tcell.Event:
			ui.HandleEvent(event)
		case *libui.AercFuncMsg:
			event.Func()
		case types.WorkerMessage:
			aerc.HandleMessage(event)
		}
		if ui.ShouldExit() {
			break
		}
		ui.Render()
	}
	err = aerc.CloseBackends()
	if err != nil {
		log.Warnf("failed to close backends: %v", err)
	}
}
