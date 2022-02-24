package widgets

import (
	"fmt"
	"log"
	"math"
	"strings"

	sortthread "github.com/emersion/go-imap-sortthread"
	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"

	"git.sr.ht/~rjarry/aerc/config"
	"git.sr.ht/~rjarry/aerc/lib"
	"git.sr.ht/~rjarry/aerc/lib/format"
	"git.sr.ht/~rjarry/aerc/lib/ui"
	"git.sr.ht/~rjarry/aerc/models"
	"git.sr.ht/~rjarry/aerc/worker/types"
)

type MessageList struct {
	ui.Invalidatable
	conf          *config.AercConfig
	logger        *log.Logger
	height        int
	scroll        int
	nmsgs         int
	spinner       *Spinner
	store         *lib.MessageStore
	isInitalizing bool
	aerc          *Aerc
}

func NewMessageList(conf *config.AercConfig, logger *log.Logger, aerc *Aerc) *MessageList {
	ml := &MessageList{
		conf:          conf,
		logger:        logger,
		spinner:       NewSpinner(&conf.Ui),
		isInitalizing: true,
		aerc:          aerc,
	}
	ml.spinner.OnInvalidate(func(_ ui.Drawable) {
		ml.Invalidate()
	})
	// TODO: stop spinner, probably
	ml.spinner.Start()
	return ml
}

func (ml *MessageList) Invalidate() {
	ml.DoInvalidate(ml)
}

func (ml *MessageList) Draw(ctx *ui.Context) {
	ml.height = ctx.Height()
	uiConfig := ml.aerc.SelectedAccountUiConfig()
	ctx.Fill(0, 0, ctx.Width(), ctx.Height(), ' ',
		uiConfig.GetStyle(config.STYLE_MSGLIST_DEFAULT))

	acct := ml.aerc.SelectedAccount()
	store := ml.Store()
	if store == nil || acct == nil {
		if ml.isInitalizing {
			ml.spinner.Draw(ctx)
			return
		} else {
			ml.spinner.Stop()
			ml.drawEmptyMessage(ctx)
			return
		}
	}

	ml.ensureScroll()

	needScrollbar := true
	percentVisible := float64(ctx.Height()) / float64(len(store.Uids()))
	if percentVisible >= 1.0 {
		needScrollbar = false
	}

	textWidth := ctx.Width()
	if needScrollbar {
		textWidth -= 1
	}
	if textWidth < 0 {
		textWidth = 0
	}

	var (
		needsHeaders []uint32
		row          int = 0
	)

	if uiConfig.ThreadingEnabled || store.BuildThreads() {
		threads := store.Threads
		counter := len(store.Uids())

		for i := len(threads) - 1; i >= 0; i-- {
			var lastSubject string
			threads[i].Walk(func(t *types.Thread, _ int, currentErr error) error {
				if currentErr != nil {
					return currentErr
				}
				if t.Hidden || t.Deleted {
					return nil
				}
				counter--
				if counter > len(store.Uids())-1-ml.scroll {
					//skip messages which are higher than the viewport
					return nil
				}
				msg := store.Messages[t.Uid]
				var prefix string
				var subject string
				var normalizedSubject string
				if msg != nil {
					prefix = threadPrefix(t)
					if msg.Envelope != nil {
						subject = msg.Envelope.Subject
						normalizedSubject, _ = sortthread.GetBaseSubject(subject)
					}
				}
				fmtCtx := format.Ctx{
					FromAddress:       acct.acct.From,
					AccountName:       acct.Name(),
					MsgInfo:           msg,
					MsgNum:            row,
					MsgIsMarked:       store.IsMarked(t.Uid),
					ThreadPrefix:      prefix,
					ThreadSameSubject: normalizedSubject == lastSubject,
				}
				if ml.drawRow(textWidth, ctx, t.Uid, row, &needsHeaders, fmtCtx) {
					return types.ErrSkipThread
				}
				lastSubject = normalizedSubject
				row++
				return nil
			})
			if row >= ctx.Height() {
				break
			}
		}
	} else {
		uids := store.Uids()
		for i := len(uids) - 1 - ml.scroll; i >= 0; i-- {
			uid := uids[i]
			msg := store.Messages[uid]
			fmtCtx := format.Ctx{
				FromAddress: acct.acct.From,
				AccountName: acct.Name(),
				MsgInfo:     msg,
				MsgNum:      row,
				MsgIsMarked: store.IsMarked(uid),
			}
			if ml.drawRow(textWidth, ctx, uid, row, &needsHeaders, fmtCtx) {
				break
			}
			row += 1
		}
	}

	if needScrollbar {
		scrollbarCtx := ctx.Subcontext(ctx.Width()-1, 0, 1, ctx.Height())
		ml.drawScrollbar(scrollbarCtx, percentVisible)
	}

	if len(store.Uids()) == 0 {
		if store.Sorting {
			ml.spinner.Start()
			ml.spinner.Draw(ctx)
			return
		} else {
			ml.drawEmptyMessage(ctx)
		}
	}

	if len(needsHeaders) != 0 {
		store.FetchHeaders(needsHeaders, nil)
		ml.spinner.Start()
	} else {
		ml.spinner.Stop()
	}
}

func (ml *MessageList) drawRow(textWidth int, ctx *ui.Context, uid uint32, row int, needsHeaders *[]uint32, fmtCtx format.Ctx) bool {
	store := ml.store
	msg := store.Messages[uid]
	acct := ml.aerc.SelectedAccount()

	if row >= ctx.Height() || acct == nil {
		return true
	}

	if msg == nil {
		*needsHeaders = append(*needsHeaders, uid)
		ml.spinner.Draw(ctx.Subcontext(0, row, textWidth, 1))
		return false
	}

	confParams := map[config.ContextType]string{
		config.UI_CONTEXT_ACCOUNT: acct.AccountConfig().Name,
		config.UI_CONTEXT_FOLDER:  acct.Directories().Selected(),
	}
	if msg.Envelope != nil {
		confParams[config.UI_CONTEXT_SUBJECT] = msg.Envelope.Subject
	}
	uiConfig := ml.conf.GetUiConfig(confParams)

	msg_styles := []config.StyleObject{}
	// unread message
	seen := false
	flagged := false
	for _, flag := range msg.Flags {
		switch flag {
		case models.SeenFlag:
			seen = true
		case models.FlaggedFlag:
			flagged = true
		}
	}

	if seen {
		msg_styles = append(msg_styles, config.STYLE_MSGLIST_READ)
	} else {
		msg_styles = append(msg_styles, config.STYLE_MSGLIST_UNREAD)
	}

	if flagged {
		msg_styles = append(msg_styles, config.STYLE_MSGLIST_FLAGGED)
	}

	// deleted message
	if _, ok := store.Deleted[msg.Uid]; ok {
		msg_styles = append(msg_styles, config.STYLE_MSGLIST_DELETED)
	}

	// marked message
	if store.IsMarked(msg.Uid) {
		msg_styles = append(msg_styles, config.STYLE_MSGLIST_MARKED)
	}

	var style tcell.Style
	// current row
	if row == ml.store.SelectedIndex()-ml.scroll {
		style = uiConfig.GetComposedStyleSelected(config.STYLE_MSGLIST_DEFAULT, msg_styles)
	} else {
		style = uiConfig.GetComposedStyle(config.STYLE_MSGLIST_DEFAULT, msg_styles)
	}

	ctx.Fill(0, row, ctx.Width(), 1, ' ', style)
	fmtStr, args, err := format.ParseMessageFormat(
		uiConfig.IndexFormat, uiConfig.TimestampFormat,
		uiConfig.ThisDayTimeFormat,
		uiConfig.ThisWeekTimeFormat,
		uiConfig.ThisYearTimeFormat,
		fmtCtx)
	if err != nil {
		ctx.Printf(0, row, style, "%v", err)
	} else {
		line := fmt.Sprintf(fmtStr, args...)
		line = runewidth.Truncate(line, textWidth, "…")
		ctx.Printf(0, row, style, "%s", line)
	}

	return false
}

func (ml *MessageList) drawScrollbar(ctx *ui.Context, percentVisible float64) {
	gutterStyle := tcell.StyleDefault
	pillStyle := tcell.StyleDefault.Reverse(true)

	// gutter
	ctx.Fill(0, 0, 1, ctx.Height(), ' ', gutterStyle)

	// pill
	pillSize := int(math.Ceil(float64(ctx.Height()) * percentVisible))
	percentScrolled := float64(ml.scroll) / float64(len(ml.Store().Uids()))
	pillOffset := int(math.Floor(float64(ctx.Height()) * percentScrolled))
	ctx.Fill(0, pillOffset, 1, pillSize, ' ', pillStyle)
}

func (ml *MessageList) MouseEvent(localX int, localY int, event tcell.Event) {
	switch event := event.(type) {
	case *tcell.EventMouse:
		switch event.Buttons() {
		case tcell.Button1:
			if ml.aerc == nil {
				return
			}
			selectedMsg, ok := ml.Clicked(localX, localY)
			if ok {
				ml.Select(selectedMsg)
				acct := ml.aerc.SelectedAccount()
				if acct == nil || acct.Messages().Empty() {
					return
				}
				store := acct.Messages().Store()
				msg := acct.Messages().Selected()
				if msg == nil {
					return
				}
				lib.NewMessageStoreView(msg, store, ml.aerc.DecryptKeys,
					func(view lib.MessageView, err error) {
						if err != nil {
							ml.aerc.PushError(err.Error())
							return
						}
						viewer := NewMessageViewer(acct, ml.aerc.Config(), view)
						ml.aerc.NewTab(viewer, msg.Envelope.Subject)
					})
			}
		case tcell.WheelDown:
			if ml.store != nil {
				ml.store.Next()
			}
			ml.Invalidate()
		case tcell.WheelUp:
			if ml.store != nil {
				ml.store.Prev()
			}
			ml.Invalidate()
		}
	}
}

func (ml *MessageList) Clicked(x, y int) (int, bool) {
	store := ml.Store()
	if store == nil || ml.nmsgs == 0 || y >= ml.nmsgs {
		return 0, false
	}
	return y + ml.scroll, true
}

func (ml *MessageList) Height() int {
	return ml.height
}

func (ml *MessageList) storeUpdate(store *lib.MessageStore) {
	if ml.Store() != store {
		return
	}
	uids := store.Uids()

	if len(uids) > 0 {
		// When new messages come in, advance the cursor accordingly
		// Note that this assumes new messages are appended to the top, which
		// isn't necessarily true once we implement SORT... ideally we'd look
		// for the previously selected UID.
		if len(uids) > ml.nmsgs && ml.nmsgs != 0 {
			for i := 0; i < len(uids)-ml.nmsgs; i++ {
				ml.Store().Next()
			}
		}
		if len(uids) < ml.nmsgs && ml.nmsgs != 0 {
			for i := 0; i < ml.nmsgs-len(uids); i++ {
				ml.Store().Prev()
			}
		}
		ml.nmsgs = len(uids)
	}

	ml.Invalidate()
}

func (ml *MessageList) SetStore(store *lib.MessageStore) {
	if ml.Store() != store {
		ml.scroll = 0
	}
	ml.store = store
	if store != nil {
		ml.spinner.Stop()
		ml.nmsgs = len(store.Uids())
		store.OnUpdate(ml.storeUpdate)
	} else {
		ml.spinner.Start()
	}
	ml.Invalidate()
}

func (ml *MessageList) SetInitDone() {
	ml.isInitalizing = false
}

func (ml *MessageList) Store() *lib.MessageStore {
	return ml.store
}

func (ml *MessageList) Empty() bool {
	store := ml.Store()
	return store == nil || len(store.Uids()) == 0
}

func (ml *MessageList) Selected() *models.MessageInfo {
	store := ml.Store()
	uids := store.Uids()
	return store.Messages[uids[len(uids)-ml.store.SelectedIndex()-1]]
}

func (ml *MessageList) Select(index int) {
	store := ml.Store()
	store.Select(index)
	ml.Invalidate()
}

func (ml *MessageList) ensureScroll() {
	store := ml.Store()
	if store == nil || len(store.Uids()) == 0 {
		return
	}

	h := ml.Height()

	maxScroll := len(store.Uids()) - h
	if maxScroll < 0 {
		maxScroll = 0
	}

	selectedIndex := store.SelectedIndex()

	if selectedIndex >= ml.scroll && selectedIndex < ml.scroll+h {
		if ml.scroll > maxScroll {
			ml.scroll = maxScroll
		}
		return
	}

	if selectedIndex >= ml.scroll+h {
		ml.scroll = selectedIndex - h + 1
	} else if selectedIndex < ml.scroll {
		ml.scroll = selectedIndex
	}

	if ml.scroll > maxScroll {
		ml.scroll = maxScroll
	}
}

func (ml *MessageList) drawEmptyMessage(ctx *ui.Context) {
	uiConfig := ml.aerc.SelectedAccountUiConfig()
	msg := uiConfig.EmptyMessage
	ctx.Printf((ctx.Width()/2)-(len(msg)/2), 0,
		uiConfig.GetStyle(config.STYLE_MSGLIST_DEFAULT), "%s", msg)
}

func threadPrefix(t *types.Thread) string {
	var arrow string
	if t.Parent != nil {
		if t.NextSibling != nil {
			arrow = "├─>"
		} else {
			arrow = "└─>"
		}
	}
	var prefix []string
	for n := t; n.Parent != nil; n = n.Parent {
		if n.Parent.NextSibling != nil {
			prefix = append(prefix, "│  ")
		} else {
			prefix = append(prefix, "   ")
		}
	}
	// prefix is now in a reverse order (inside --> outside), so turn it
	for i, j := 0, len(prefix)-1; i < j; i, j = i+1, j-1 {
		prefix[i], prefix[j] = prefix[j], prefix[i]
	}

	// we don't want to indent the first child, hence we strip that level
	if len(prefix) > 0 {
		prefix = prefix[1:]
	}
	ps := strings.Join(prefix, "")
	return fmt.Sprintf("%v%v", ps, arrow)
}
