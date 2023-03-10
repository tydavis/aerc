package msg

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path"
	"strings"
	"sync"

	"git.sr.ht/~rjarry/aerc/config"
	"git.sr.ht/~rjarry/aerc/lib"
	"git.sr.ht/~rjarry/aerc/lib/format"
	"git.sr.ht/~rjarry/aerc/log"
	"git.sr.ht/~rjarry/aerc/models"
	"git.sr.ht/~rjarry/aerc/widgets"
	"git.sr.ht/~rjarry/aerc/worker/types"
	"github.com/emersion/go-message/mail"

	"git.sr.ht/~sircmpwn/getopt"
)

type forward struct{}

func init() {
	register(forward{})
}

func (forward) Aliases() []string {
	return []string{"forward"}
}

func (forward) Complete(aerc *widgets.Aerc, args []string) []string {
	return nil
}

func (forward) Execute(aerc *widgets.Aerc, args []string) error {
	opts, optind, err := getopt.Getopts(args, "AFT:")
	if err != nil {
		return err
	}
	attachAll := false
	attachFull := false
	template := ""
	for _, opt := range opts {
		switch opt.Option {
		case 'A':
			attachAll = true
		case 'F':
			attachFull = true
		case 'T':
			template = opt.Value
		}
	}

	if attachAll && attachFull {
		return errors.New("Options -A and -F are mutually exclusive")
	}

	widget := aerc.SelectedTabContent().(widgets.ProvidesMessage)
	acct := widget.SelectedAccount()
	if acct == nil {
		return errors.New("No account selected")
	}
	store := widget.Store()
	if store == nil {
		return errors.New("Cannot perform action. Messages still loading")
	}
	msg, err := widget.SelectedMessage()
	if err != nil {
		return err
	}
	log.Debugf("Forwarding email <%s>", msg.Envelope.MessageId)

	h := &mail.Header{}
	subject := "Fwd: " + msg.Envelope.Subject
	h.SetSubject(subject)

	var tolist []*mail.Address
	to := strings.Join(args[optind:], ", ")
	if strings.Contains(to, "@") {
		tolist, err = mail.ParseAddressList(to)
		if err != nil {
			return fmt.Errorf("invalid to address(es): %w", err)
		}
	}
	if len(tolist) > 0 {
		h.SetAddressList("to", tolist)
	}

	original := models.OriginalMail{
		From:          format.FormatAddresses(msg.Envelope.From),
		Date:          msg.Envelope.Date,
		RFC822Headers: msg.RFC822Headers,
	}

	addTab := func() (*widgets.Composer, error) {
		composer, err := widgets.NewComposer(aerc, acct,
			acct.AccountConfig(), acct.Worker(), template, h, &original)
		if err != nil {
			aerc.PushError("Error: " + err.Error())
			return nil, err
		}

		composer.Tab = aerc.NewTab(composer, subject)
		if !h.Has("to") {
			composer.FocusEditor("to")
		} else {
			composer.FocusTerminal()
		}
		return composer, nil
	}

	if attachFull {
		tmpDir, err := os.MkdirTemp("", "aerc-tmp-attachment")
		if err != nil {
			return err
		}
		tmpFileName := path.Join(tmpDir,
			strings.ReplaceAll(fmt.Sprintf("%s.eml", msg.Envelope.Subject), "/", "-"))
		store.FetchFull([]uint32{msg.Uid}, func(fm *types.FullMessage) {
			tmpFile, err := os.Create(tmpFileName)
			if err != nil {
				log.Warnf("failed to create temporary attachment: %v", err)
				_, err = addTab()
				if err != nil {
					log.Warnf("failed to add tab: %v", err)
				}
				return
			}

			defer tmpFile.Close()
			_, err = io.Copy(tmpFile, fm.Content.Reader)
			if err != nil {
				log.Warnf("failed to write to tmpfile: %v", err)
				return
			}
			composer, err := addTab()
			if err != nil {
				return
			}
			composer.AddAttachment(tmpFileName)
			composer.OnClose(func(_ *widgets.Composer) {
				os.RemoveAll(tmpDir)
			})
		})
	} else {
		if template == "" {
			template = config.Templates.Forwards
		}

		part := lib.FindPlaintext(msg.BodyStructure, nil)
		if part == nil {
			part = lib.FindFirstNonMultipart(msg.BodyStructure, nil)
			// if it's still nil here, we don't have a multipart msg, that's fine
		}
		err = addMimeType(msg, part, &original)
		if err != nil {
			return err
		}
		store.FetchBodyPart(msg.Uid, part, func(reader io.Reader) {
			buf := new(bytes.Buffer)
			scanner := bufio.NewScanner(reader)
			for scanner.Scan() {
				buf.WriteString(scanner.Text() + "\n")
			}
			original.Text = buf.String()

			// create composer
			composer, err := addTab()
			if err != nil {
				return
			}

			// add attachments
			if attachAll {
				var mu sync.Mutex
				parts := lib.FindAllNonMultipart(msg.BodyStructure, nil, nil)
				for _, p := range parts {
					if lib.EqualParts(p, part) {
						continue
					}
					bs, err := msg.BodyStructure.PartAtIndex(p)
					if err != nil {
						log.Errorf("cannot get PartAtIndex %v: %v", p, err)
						continue
					}
					store.FetchBodyPart(msg.Uid, p, func(reader io.Reader) {
						mime := bs.FullMIMEType()
						params := lib.SetUtf8Charset(bs.Params)
						name, ok := params["name"]
						if !ok {
							name = fmt.Sprintf("%s_%s_%d", bs.MIMEType, bs.MIMESubType, rand.Uint64())
						}
						mu.Lock()
						err := composer.AddPartAttachment(name, mime, params, reader)
						mu.Unlock()
						if err != nil {
							log.Errorf(err.Error())
							aerc.PushError(err.Error())
						}
					})
				}
			}
		})
	}
	return nil
}
