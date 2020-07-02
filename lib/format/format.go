package format

import (
	"errors"
	"fmt"
	gomail "net/mail"
	"strings"
	"unicode"

	"git.sr.ht/~sircmpwn/aerc/models"
)

func parseAddress(address string) *gomail.Address {
	addrs, err := gomail.ParseAddress(address)
	if err != nil {
		return nil
	}

	return addrs
}

func ParseMessageFormat(
	fromAddress string,
	format string, timestampformat string,
	accountName string, number int, msg *models.MessageInfo,
	marked bool) (string,
	[]interface{}, error) {
	retval := make([]byte, 0, len(format))
	var args []interface{}

	accountFromAddress := parseAddress(fromAddress)

	var c rune
	for i, ni := 0, 0; i < len(format); {
		ni = strings.IndexByte(format[i:], '%')
		if ni < 0 {
			ni = len(format)
			retval = append(retval, []byte(format[i:ni])...)
			break
		}
		ni += i + 1
		// Check for fmt flags
		if ni == len(format) {
			goto handle_end_error
		}
		c = rune(format[ni])
		if c == '+' || c == '-' || c == '#' || c == ' ' || c == '0' {
			ni++
		}

		// Check for precision and width
		if ni == len(format) {
			goto handle_end_error
		}
		c = rune(format[ni])
		for unicode.IsDigit(c) {
			ni++
			c = rune(format[ni])
		}
		if c == '.' {
			ni++
			c = rune(format[ni])
			for unicode.IsDigit(c) {
				ni++
				c = rune(format[ni])
			}
		}

		retval = append(retval, []byte(format[i:ni])...)
		// Get final format verb
		if ni == len(format) {
			goto handle_end_error
		}
		c = rune(format[ni])
		switch c {
		case '%':
			retval = append(retval, '%')
		case 'a':
			if msg.Envelope == nil {
				return "", nil,
					errors.New("no envelope available for this message")
			}
			if len(msg.Envelope.From) == 0 {
				return "", nil,
					errors.New("found no address for sender")
			}
			addr := msg.Envelope.From[0]
			retval = append(retval, 's')
			args = append(args,
				fmt.Sprintf("%s@%s", addr.Mailbox, addr.Host))
		case 'A':
			if msg.Envelope == nil {
				return "", nil,
					errors.New("no envelope available for this message")
			}
			var addr *models.Address
			if len(msg.Envelope.ReplyTo) == 0 {
				if len(msg.Envelope.From) == 0 {
					return "", nil,
						errors.New("found no address for sender or reply-to")
				} else {
					addr = msg.Envelope.From[0]
				}
			} else {
				addr = msg.Envelope.ReplyTo[0]
			}
			retval = append(retval, 's')
			args = append(args,
				fmt.Sprintf("%s@%s", addr.Mailbox, addr.Host))
		case 'C':
			retval = append(retval, 'd')
			args = append(args, number)
		case 'd':
			retval = append(retval, 's')
			args = append(args,
				msg.InternalDate.Format(timestampformat))
		case 'D':
			retval = append(retval, 's')
			args = append(args,
				msg.InternalDate.Local().Format(timestampformat))
		case 'f':
			if msg.Envelope == nil {
				return "", nil,
					errors.New("no envelope available for this message")
			}
			if len(msg.Envelope.From) == 0 {
				return "", nil,
					errors.New("found no address for sender")
			}
			addr := msg.Envelope.From[0].Format()
			retval = append(retval, 's')
			args = append(args, addr)
		case 'F':
			if msg.Envelope == nil {
				return "", nil,
					errors.New("no envelope available for this message")
			}
			if len(msg.Envelope.From) == 0 {
				return "", nil,
					errors.New("found no address for sender")
			}
			addr := msg.Envelope.From[0]
			var val string

			if addr.Name == accountFromAddress.Name && len(msg.Envelope.To) != 0 {
				addr = msg.Envelope.To[0]
			}

			if addr.Name != "" {
				val = addr.Name
			} else {
				val = fmt.Sprintf("%s@%s", addr.Mailbox, addr.Host)
			}
			retval = append(retval, 's')
			args = append(args, val)

		case 'g':
			retval = append(retval, 's')
			args = append(args, strings.Join(msg.Labels, ", "))

		case 'i':
			if msg.Envelope == nil {
				return "", nil,
					errors.New("no envelope available for this message")
			}
			retval = append(retval, 's')
			args = append(args, msg.Envelope.MessageId)
		case 'n':
			if msg.Envelope == nil {
				return "", nil,
					errors.New("no envelope available for this message")
			}
			if len(msg.Envelope.From) == 0 {
				return "", nil,
					errors.New("found no address for sender")
			}
			addr := msg.Envelope.From[0]
			var val string
			if addr.Name != "" {
				val = addr.Name
			} else {
				val = fmt.Sprintf("%s@%s", addr.Mailbox, addr.Host)
			}
			retval = append(retval, 's')
			args = append(args, val)
		case 'r':
			if msg.Envelope == nil {
				return "", nil,
					errors.New("no envelope available for this message")
			}
			addrs := models.FormatAddresses(msg.Envelope.To)
			retval = append(retval, 's')
			args = append(args, addrs)
		case 'R':
			if msg.Envelope == nil {
				return "", nil,
					errors.New("no envelope available for this message")
			}
			addrs := models.FormatAddresses(msg.Envelope.Cc)
			retval = append(retval, 's')
			args = append(args, addrs)
		case 's':
			if msg.Envelope == nil {
				return "", nil,
					errors.New("no envelope available for this message")
			}
			retval = append(retval, 's')
			args = append(args, msg.Envelope.Subject)
		case 't':
			if msg.Envelope == nil {
				return "", nil,
					errors.New("no envelope available for this message")
			}
			if len(msg.Envelope.To) == 0 {
				return "", nil,
					errors.New("found no address for recipient")
			}
			addr := msg.Envelope.To[0]
			retval = append(retval, 's')
			args = append(args,
				fmt.Sprintf("%s@%s", addr.Mailbox, addr.Host))
		case 'T':
			retval = append(retval, 's')
			args = append(args, accountName)
		case 'u':
			if msg.Envelope == nil {
				return "", nil,
					errors.New("no envelope available for this message")
			}
			if len(msg.Envelope.From) == 0 {
				return "", nil,
					errors.New("found no address for sender")
			}
			addr := msg.Envelope.From[0]
			retval = append(retval, 's')
			args = append(args, addr.Mailbox)
		case 'v':
			if msg.Envelope == nil {
				return "", nil,
					errors.New("no envelope available for this message")
			}
			if len(msg.Envelope.From) == 0 {
				return "", nil,
					errors.New("found no address for sender")
			}
			addr := msg.Envelope.From[0]
			// check if message is from current user
			if addr.Name != "" {
				retval = append(retval, 's')
				args = append(args,
					strings.Split(addr.Name, " ")[0])
			}
		case 'Z':
			// calculate all flags
			var readReplyFlag = ""
			var delFlag = ""
			var flaggedFlag = ""
			var markedFlag = ""
			seen := false
			recent := false
			answered := false
			for _, flag := range msg.Flags {
				if flag == models.SeenFlag {
					seen = true
				} else if flag == models.RecentFlag {
					recent = true
				} else if flag == models.AnsweredFlag {
					answered = true
				}
				if flag == models.DeletedFlag {
					delFlag = "D"
					// TODO: check if attachments
				}
				if flag == models.FlaggedFlag {
					flaggedFlag = "!"
				}
				// TODO: check gpg stuff
			}
			if seen {
				if answered {
					readReplyFlag = "r" // message has been replied to
				}
			} else {
				if recent {
					readReplyFlag = "N" // message is new
				} else {
					readReplyFlag = "O" // message is old
				}
			}
			if marked {
				markedFlag = "*"
			}
			retval = append(retval, '4', 's')
			args = append(args, readReplyFlag+delFlag+flaggedFlag+markedFlag)

		// Move the below cases to proper alphabetical positions once
		// implemented
		case 'l':
			// TODO: number of lines in the message
			retval = append(retval, 'd')
			args = append(args, msg.Size)
		case 'e':
			// TODO: current message number in thread
			fallthrough
		case 'E':
			// TODO: number of messages in current thread
			fallthrough
		case 'H':
			// TODO: spam attribute(s) of this message
			fallthrough
		case 'L':
			// TODO:
			fallthrough
		case 'X':
			// TODO: number of attachments
			fallthrough
		case 'y':
			// TODO: X-Label field
			fallthrough
		case 'Y':
			// TODO: X-Label field and some other constraints
			fallthrough
		default:
			// Just ignore it and print as is
			// so %k in index format becomes %%k to Printf
			retval = append(retval, '%')
			retval = append(retval, byte(c))
		}
		i = ni + 1
	}

	return string(retval), args, nil

handle_end_error:
	return "", nil,
		errors.New("reached end of string while parsing message format")
}
