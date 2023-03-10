package commands

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"unicode"

	"github.com/google/shlex"

	"git.sr.ht/~rjarry/aerc/config"
	"git.sr.ht/~rjarry/aerc/lib/state"
	"git.sr.ht/~rjarry/aerc/lib/templates"
	"git.sr.ht/~rjarry/aerc/log"
	"git.sr.ht/~rjarry/aerc/models"
	"git.sr.ht/~rjarry/aerc/widgets"
)

type Command interface {
	Aliases() []string
	Execute(*widgets.Aerc, []string) error
	Complete(*widgets.Aerc, []string) []string
}

type Commands map[string]Command

func NewCommands() *Commands {
	cmds := Commands(make(map[string]Command))
	return &cmds
}

func (cmds *Commands) dict() map[string]Command {
	return map[string]Command(*cmds)
}

func (cmds *Commands) Names() []string {
	names := make([]string, 0)

	for k := range cmds.dict() {
		names = append(names, k)
	}
	return names
}

func (cmds *Commands) ByName(name string) Command {
	if cmd, ok := cmds.dict()[name]; ok {
		return cmd
	}
	return nil
}

func (cmds *Commands) Register(cmd Command) {
	// TODO enforce unique aliases, until then, duplicate each
	if len(cmd.Aliases()) < 1 {
		return
	}
	for _, alias := range cmd.Aliases() {
		cmds.dict()[alias] = cmd
	}
}

type NoSuchCommand string

func (err NoSuchCommand) Error() string {
	return "Unknown command " + string(err)
}

type CommandSource interface {
	Commands() *Commands
}

func templateData(
	aerc *widgets.Aerc,
	cfg *config.AccountConfig,
	msg *models.MessageInfo,
) models.TemplateData {
	var folder string

	acct := aerc.SelectedAccount()
	if acct != nil {
		folder = acct.SelectedDirectory()
	}
	if cfg == nil && acct != nil {
		cfg = acct.AccountConfig()
	}
	if msg == nil && acct != nil {
		msg, _ = acct.SelectedMessage()
	}

	var data state.TemplateData

	data.SetAccount(cfg)
	data.SetFolder(folder)
	data.SetInfo(msg, 0, false)

	return &data
}

func (cmds *Commands) ExecuteCommand(
	aerc *widgets.Aerc,
	args []string,
	account *config.AccountConfig,
	msg *models.MessageInfo,
) error {
	if len(args) == 0 {
		return errors.New("Expected a command.")
	}
	if cmd, ok := cmds.dict()[args[0]]; ok {
		log.Tracef("executing command %v", args)
		var buf bytes.Buffer
		data := templateData(aerc, account, msg)

		processedArgs := make([]string, len(args))
		for i, arg := range args {
			t, err := templates.ParseTemplate(arg, arg)
			if err != nil {
				return err
			}
			err = templates.Render(t, &buf, data)
			if err != nil {
				return err
			}
			arg = buf.String()
			buf.Reset()
			processedArgs[i] = arg
		}

		return cmd.Execute(aerc, processedArgs)
	}
	return NoSuchCommand(args[0])
}

func (cmds *Commands) GetCompletions(aerc *widgets.Aerc, cmd string) []string {
	args, err := shlex.Split(cmd)
	if err != nil {
		return nil
	}

	// nothing entered, list all commands
	if len(args) == 0 {
		names := cmds.Names()
		sort.Strings(names)
		return names
	}

	// complete options
	if len(args) > 1 || cmd[len(cmd)-1] == ' ' {
		if cmd, ok := cmds.dict()[args[0]]; ok {
			var completions []string
			if len(args) > 1 {
				completions = cmd.Complete(aerc, args[1:])
			} else {
				completions = cmd.Complete(aerc, []string{})
			}
			if completions != nil && len(completions) == 0 {
				return nil
			}

			options := make([]string, 0)
			for _, option := range completions {
				options = append(options, args[0]+" "+option)
			}
			return options
		}
		return nil
	}

	// complete available commands
	names := cmds.Names()
	options := FilterList(names, args[0], "", aerc.SelectedAccountUiConfig().FuzzyComplete)

	if len(options) > 0 {
		return options
	}
	return nil
}

func GetFolders(aerc *widgets.Aerc, args []string) []string {
	acct := aerc.SelectedAccount()
	if acct == nil {
		return make([]string, 0)
	}
	if len(args) == 0 {
		return acct.Directories().List()
	}
	return FilterList(acct.Directories().List(), args[0], "", acct.UiConfig().FuzzyComplete)
}

// CompletionFromList provides a convenience wrapper for commands to use in the
// Complete function. It simply matches the items provided in valid
func CompletionFromList(aerc *widgets.Aerc, valid []string, args []string) []string {
	if len(args) == 0 {
		return valid
	}
	return FilterList(valid, args[0], "", aerc.SelectedAccountUiConfig().FuzzyComplete)
}

func GetLabels(aerc *widgets.Aerc, args []string) []string {
	acct := aerc.SelectedAccount()
	if acct == nil {
		return make([]string, 0)
	}
	if len(args) == 0 {
		return acct.Labels()
	}

	// + and - are used to denote tag addition / removal and need to be striped
	// only the last tag should be completed, so that multiple labels can be
	// selected
	last := args[len(args)-1]
	others := strings.Join(args[:len(args)-1], " ")
	var prefix string
	switch last[0] {
	case '+':
		prefix = "+"
	case '-':
		prefix = "-"
	default:
		prefix = ""
	}
	trimmed := strings.TrimLeft(last, "+-")

	var prev string
	if len(others) > 0 {
		prev = others + " "
	}
	out := FilterList(acct.Labels(), trimmed, prev+prefix, acct.UiConfig().FuzzyComplete)
	return out
}

// hasCaseSmartPrefix checks whether s starts with prefix, using a case
// sensitive match if and only if prefix contains upper case letters.
func hasCaseSmartPrefix(s, prefix string) bool {
	if hasUpper(prefix) {
		return strings.HasPrefix(s, prefix)
	}
	return strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix))
}

func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}
