package models

import (
	"time"

	"github.com/emersion/go-message/mail"
)

// This interface needs to be implemented for compliance with aerc-templates(7)
type TemplateData interface {
	Account() string
	Folder() string
	To() []*mail.Address
	Cc() []*mail.Address
	Bcc() []*mail.Address
	From() []*mail.Address
	Peer() []*mail.Address
	ReplyTo() []*mail.Address
	Date() time.Time
	DateAutoFormat(date time.Time) string
	Header(name string) string
	ThreadPrefix() string
	Subject() string
	SubjectBase() string
	Number() int
	Labels() []string
	Flags() []string
	MessageId() string
	Size() int
	OriginalText() string
	OriginalDate() time.Time
	OriginalFrom() []*mail.Address
	OriginalMIMEType() string
	OriginalHeader(name string) string
	Recent(folders ...string) int
	Unread(folders ...string) int
	Exists(folders ...string) int
	RUE(folders ...string) string
	Connected() bool
	ConnectionInfo() string
	ContentInfo() string
	StatusInfo() string
	TrayInfo() string
	PendingKeys() string
	Style(string, string) string
	StyleSwitch(string, ...Case) string
}

type Case interface {
	Matches(string) bool
	Value() string
}
