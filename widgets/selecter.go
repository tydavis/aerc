package widgets

import (
	"github.com/gdamore/tcell"

	"git.sr.ht/~sircmpwn/aerc/lib/ui"
)

type Selecter struct {
	ui.Invalidatable
	chooser bool
	focused bool
	focus   int
	options []string

	onChoose func(option string)
	onSelect func(option string)
}

func NewSelecter(options []string, focus int) *Selecter {
	return &Selecter{
		focus:   focus,
		options: options,
	}
}

func (sel *Selecter) Chooser(chooser bool) *Selecter {
	sel.chooser = chooser
	return sel
}

func (sel *Selecter) Invalidate() {
	sel.DoInvalidate(sel)
}

func (sel *Selecter) Draw(ctx *ui.Context) {
	ctx.Fill(0, 0, ctx.Width(), ctx.Height(), ' ', tcell.StyleDefault)
	x := 2
	for i, option := range sel.options {
		style := tcell.StyleDefault
		if sel.focus == i {
			if sel.focused {
				style = style.Reverse(true)
			} else if sel.chooser {
				style = style.Bold(true)
			}
		}
		x += ctx.Printf(x, 1, style, "[%s]", option)
		x += 5
	}
}

func (sel *Selecter) OnChoose(fn func(option string)) *Selecter {
	sel.onChoose = fn
	return sel
}

func (sel *Selecter) OnSelect(fn func(option string)) *Selecter {
	sel.onSelect = fn
	return sel
}

func (sel *Selecter) Selected() string {
	return sel.options[sel.focus]
}

func (sel *Selecter) Focus(focus bool) {
	sel.focused = focus
	sel.Invalidate()
}

func (sel *Selecter) Event(event tcell.Event) bool {
	switch event := event.(type) {
	case *tcell.EventKey:
		switch event.Key() {
		case tcell.KeyCtrlH:
			fallthrough
		case tcell.KeyLeft:
			if sel.focus > 0 {
				sel.focus--
				sel.Invalidate()
			}
			if sel.onSelect != nil {
				sel.onSelect(sel.Selected())
			}
		case tcell.KeyCtrlL:
			fallthrough
		case tcell.KeyRight:
			if sel.focus < len(sel.options)-1 {
				sel.focus++
				sel.Invalidate()
			}
			if sel.onSelect != nil {
				sel.onSelect(sel.Selected())
			}
		case tcell.KeyEnter:
			if sel.onChoose != nil {
				sel.onChoose(sel.Selected())
			}
		}
	}
	return false
}