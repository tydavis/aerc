package ui

import (
	"fmt"

	"git.sr.ht/~rjarry/aerc/lib/parse"
	"github.com/gdamore/tcell/v2"
	"github.com/gdamore/tcell/v2/views"
)

// A context allows you to draw in a sub-region of the terminal
type Context struct {
	screen    tcell.Screen
	viewport  *views.ViewPort
	x, y      int
	onPopover func(*Popover)
}

func (ctx *Context) X() int {
	x, _, _, _ := ctx.viewport.GetPhysical()
	return x
}

func (ctx *Context) Y() int {
	_, y, _, _ := ctx.viewport.GetPhysical()
	return y
}

func (ctx *Context) Width() int {
	width, _ := ctx.viewport.Size()
	return width
}

func (ctx *Context) Height() int {
	_, height := ctx.viewport.Size()
	return height
}

func NewContext(width, height int, screen tcell.Screen, p func(*Popover)) *Context {
	vp := views.NewViewPort(screen, 0, 0, width, height)
	return &Context{screen, vp, 0, 0, p}
}

func (ctx *Context) Subcontext(x, y, width, height int) *Context {
	vp_width, vp_height := ctx.viewport.Size()
	if x < 0 || y < 0 {
		panic(fmt.Errorf("Attempted to create context with negative offset"))
	}
	if x+width > vp_width || y+height > vp_height {
		panic(fmt.Errorf("Attempted to create context larger than parent"))
	}
	vp := views.NewViewPort(ctx.viewport, x, y, width, height)
	return &Context{ctx.screen, vp, ctx.x + x, ctx.y + y, ctx.onPopover}
}

func (ctx *Context) SetCell(x, y int, ch rune, style tcell.Style) {
	width, height := ctx.viewport.Size()
	if x >= width || y >= height {
		// no-op when dims are inadequate
		return
	}
	crunes := []rune{}
	ctx.viewport.SetContent(x, y, ch, crunes, style)
}

func (ctx *Context) Printf(x, y int, style tcell.Style,
	format string, a ...interface{},
) int {
	width, height := ctx.viewport.Size()

	if x >= width || y >= height {
		// no-op when dims are inadequate
		return 0
	}

	str := fmt.Sprintf(format, a...)

	buf := parse.ParseANSI(str)
	buf.ApplyStyle(style)

	old_x := x

	newline := func() bool {
		x = old_x
		y++
		return y < height
	}
	for _, sr := range buf.Runes() {
		switch sr.Value {
		case '\n':
			if !newline() {
				return buf.Len()
			}
		case '\r':
			x = old_x
		default:
			crunes := []rune{}
			ctx.viewport.SetContent(x, y, sr.Value, crunes, sr.Style)
			x += sr.Width
			if x == old_x+width {
				if !newline() {
					return buf.Len()
				}
			}
		}
	}

	return buf.Len()
}

func (ctx *Context) Fill(x, y, width, height int, rune rune, style tcell.Style) {
	vp := views.NewViewPort(ctx.viewport, x, y, width, height)
	vp.Fill(rune, style)
}

func (ctx *Context) SetCursor(x, y int) {
	ctx.screen.ShowCursor(ctx.x+x, ctx.y+y)
}

func (ctx *Context) SetCursorStyle(cs tcell.CursorStyle) {
	ctx.screen.SetCursorStyle(cs)
}

func (ctx *Context) HideCursor() {
	ctx.screen.HideCursor()
}

func (ctx *Context) Popover(x, y, width, height int, d Drawable) {
	ctx.onPopover(&Popover{
		x:       ctx.x + x,
		y:       ctx.y + y,
		width:   width,
		height:  height,
		content: d,
	})
}

func (ctx *Context) View() *views.ViewPort {
	return ctx.viewport
}

func (ctx *Context) Show() {
	ctx.screen.Show()
}
