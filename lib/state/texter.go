package state

import (
	"strings"

	"git.sr.ht/~rjarry/aerc/config"
)

type texterInterface interface {
	Connected() string
	Disconnected() string
	Passthrough() string
	Sorting() string
	Threading() string
	FormatFilter(string) string
	FormatSearch(string) string
}

type text struct{}

var txt text

func (t text) Connected() string {
	return "Connected"
}

func (t text) Disconnected() string {
	return "Disconnected"
}

func (t text) Passthrough() string {
	return "passthrough"
}

func (t text) Sorting() string {
	return "sorting"
}

func (t text) Threading() string {
	return "threading"
}

func (t text) FormatFilter(s string) string {
	return s
}

func (t text) FormatSearch(s string) string {
	return s
}

type icon struct{}

var icn icon

func (i icon) Connected() string {
	return "โ"
}

func (i icon) Disconnected() string {
	return "โ"
}

func (i icon) Passthrough() string {
	return "โ"
}

func (i icon) Sorting() string {
	return "โ"
}

func (i icon) Threading() string {
	return "๐งต"
}

func (i icon) FormatFilter(s string) string {
	return strings.ReplaceAll(s, "filter", "๐ฆ")
}

func (i icon) FormatSearch(s string) string {
	return strings.ReplaceAll(s, "search", "๐")
}

func texter() texterInterface {
	switch strings.ToLower(config.Statusline.DisplayMode) {
	case "icon":
		return &icn
	default:
		return &txt
	}
}
