package compose

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"git.sr.ht/~sircmpwn/getopt"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/google/shlex"
	"github.com/pkg/errors"

	"git.sr.ht/~rjarry/aerc/commands/mode"
	"git.sr.ht/~rjarry/aerc/lib"
	"git.sr.ht/~rjarry/aerc/log"
	"git.sr.ht/~rjarry/aerc/models"
	"git.sr.ht/~rjarry/aerc/widgets"
	"git.sr.ht/~rjarry/aerc/worker/types"
	"github.com/emersion/go-message/mail"
	"golang.org/x/oauth2"
)

type Send struct{}

func init() {
	register(Send{})
}

func (Send) Aliases() []string {
	return []string{"send"}
}

func (Send) Complete(aerc *widgets.Aerc, args []string) []string {
	return nil
}

func (Send) Execute(aerc *widgets.Aerc, args []string) error {
	opts, optind, err := getopt.Getopts(args, "a:")
	if err != nil {
		return err
	}
	if optind != len(args) {
		return errors.New("Usage: send [-a <flat|year|month>]")
	}
	var archive string
	for _, opt := range opts {
		if opt.Option == 'a' {
			archive = opt.Value
		}
	}
	tab := aerc.SelectedTab()
	if tab == nil {
		return errors.New("No selected tab")
	}
	composer, _ := tab.Content.(*widgets.Composer)
	tabName := tab.Name
	config := composer.Config()

	outgoing, err := config.Outgoing.ConnectionString()
	if err != nil {
		return errors.Wrap(err, "ReadCredentials(outgoing)")
	}
	if outgoing == "" {
		return errors.New(
			"No outgoing mail transport configured for this account")
	}

	header, err := composer.PrepareHeader()
	if err != nil {
		return errors.Wrap(err, "PrepareHeader")
	}
	rcpts, err := listRecipients(header)
	if err != nil {
		return errors.Wrap(err, "listRecipients")
	}

	uri, err := url.Parse(outgoing)
	if err != nil {
		return errors.Wrap(err, "url.Parse(outgoing)")
	}

	scheme, auth, err := parseScheme(uri)
	if err != nil {
		return err
	}
	var starttls bool
	if starttls_, ok := config.Params["smtp-starttls"]; ok {
		starttls = starttls_ == "yes"
	}
	var domain string
	if domain_, ok := config.Params["smtp-domain"]; ok {
		domain = domain_
	}
	ctx := sendCtx{
		uri:      uri,
		scheme:   scheme,
		auth:     auth,
		starttls: starttls,
		from:     config.From,
		rcpts:    rcpts,
		domain:   domain,
	}

	log.Debugf("send config uri: %s", ctx.uri)
	log.Debugf("send config scheme: %s", ctx.scheme)
	log.Debugf("send config auth: %s", ctx.auth)
	log.Debugf("send config starttls: %s", ctx.starttls)
	log.Debugf("send config from: %s", ctx.from)
	log.Debugf("send config rcpts: %s", ctx.rcpts)
	log.Debugf("send config domain: %s", ctx.domain)

	warn, err := composer.ShouldWarnAttachment()
	if err != nil || warn {
		msg := "You may have forgotten an attachment."
		if err != nil {
			log.Warnf("failed to check for a forgotten attachment: %v", err)
			msg = "Failed to check for a forgotten attachment."
		}

		prompt := widgets.NewPrompt(
			msg+" Abort send? [Y/n] ",
			func(text string) {
				if text == "n" || text == "N" {
					send(aerc, composer, ctx, header, tabName, archive)
				}
			}, func(cmd string) ([]string, string) {
				if cmd == "" {
					return []string{"y", "n"}, ""
				}

				return nil, ""
			},
		)

		aerc.PushPrompt(prompt)
	} else {
		send(aerc, composer, ctx, header, tabName, archive)
	}

	return nil
}

func send(aerc *widgets.Aerc, composer *widgets.Composer, ctx sendCtx,
	header *mail.Header, tabName string, archive string,
) {
	// we don't want to block the UI thread while we are sending
	// so we do everything in a goroutine and hide the composer from the user
	aerc.RemoveTab(composer)
	aerc.PushStatus("Sending...", 10*time.Second)
	log.Debugf("send uri: %s", ctx.uri.String())

	// enter no-quit mode
	mode.NoQuit()

	var copyBuf bytes.Buffer // for the Sent folder content if CopyTo is set
	config := composer.Config()

	failCh := make(chan error)
	// writer
	go func() {
		defer log.PanicHandler()

		var sender io.WriteCloser
		var err error
		switch ctx.scheme {
		case "smtp":
			fallthrough
		case "smtps":
			sender, err = newSmtpSender(ctx)
		case "":
			sender, err = newSendmailSender(ctx)
		default:
			sender, err = nil, fmt.Errorf("unsupported scheme %v", ctx.scheme)
		}
		if err != nil {
			failCh <- errors.Wrap(err, "send:")
			return
		}

		var writer io.Writer = sender

		if config.CopyTo != "" {
			writer = io.MultiWriter(writer, &copyBuf)
		}
		err = composer.WriteMessage(header, writer)
		if err != nil {
			failCh <- err
			return
		}
		failCh <- sender.Close()
	}()

	// cleanup + copy to sent
	go func() {
		defer log.PanicHandler()

		// leave no-quit mode
		defer mode.NoQuitDone()

		err := <-failCh
		if err != nil {
			aerc.PushError(strings.ReplaceAll(err.Error(), "\n", " "))
			aerc.NewTab(composer, tabName)
			return
		}
		if config.CopyTo != "" {
			aerc.PushStatus("Copying to "+config.CopyTo, 10*time.Second)
			errch := copyToSent(composer.Worker(), config.CopyTo,
				copyBuf.Len(), &copyBuf)
			err = <-errch
			if err != nil {
				errmsg := fmt.Sprintf(
					"message sent, but copying to %v failed: %v",
					config.CopyTo, err.Error())
				aerc.PushError(errmsg)
				composer.SetSent(archive)
				composer.Close()
				return
			}
		}
		aerc.PushStatus("Message sent.", 10*time.Second)
		composer.SetSent(archive)
		composer.Close()
	}()
}

func listRecipients(h *mail.Header) ([]*mail.Address, error) {
	var rcpts []*mail.Address
	for _, key := range []string{"to", "cc", "bcc"} {
		list, err := h.AddressList(key)
		if err != nil {
			return nil, err
		}
		rcpts = append(rcpts, list...)
	}
	return rcpts, nil
}

type sendCtx struct {
	uri      *url.URL
	scheme   string
	auth     string
	starttls bool
	from     *mail.Address
	rcpts    []*mail.Address
	domain   string
}

func newSendmailSender(ctx sendCtx) (io.WriteCloser, error) {
	args, err := shlex.Split(ctx.uri.Path)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("no command specified")
	}
	bin := args[0]
	rs := make([]string, len(ctx.rcpts))
	for i := range ctx.rcpts {
		rs[i] = ctx.rcpts[i].Address
	}
	args = append(args[1:], rs...)
	cmd := exec.Command(bin, args...)
	s := &sendmailSender{cmd: cmd}
	s.stdin, err = s.cmd.StdinPipe()
	if err != nil {
		return nil, errors.Wrap(err, "cmd.StdinPipe")
	}
	err = s.cmd.Start()
	if err != nil {
		return nil, errors.Wrap(err, "cmd.Start")
	}
	return s, nil
}

type sendmailSender struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

func (s *sendmailSender) Write(p []byte) (int, error) {
	return s.stdin.Write(p)
}

func (s *sendmailSender) Close() error {
	se := s.stdin.Close()
	ce := s.cmd.Wait()
	if se != nil {
		return se
	}
	return ce
}

func parseScheme(uri *url.URL) (scheme string, auth string, err error) {
	scheme = ""
	auth = "plain"
	if uri.Scheme != "" {
		parts := strings.Split(uri.Scheme, "+")
		switch len(parts) {
		case 1:
			scheme = parts[0]
		case 2:
			scheme = parts[0]
			auth = parts[1]
		default:
			return "", "", fmt.Errorf("Unknown transfer protocol %s", uri.Scheme)
		}
	}
	return scheme, auth, nil
}

func newSaslClient(auth string, uri *url.URL) (sasl.Client, error) {
	var saslClient sasl.Client
	switch auth {
	case "":
		fallthrough
	case "none":
		saslClient = nil
	case "login":
		password, _ := uri.User.Password()
		saslClient = sasl.NewLoginClient(uri.User.Username(), password)
	case "plain":
		password, _ := uri.User.Password()
		saslClient = sasl.NewPlainClient("", uri.User.Username(), password)
	case "oauthbearer":
		q := uri.Query()
		oauth2 := &oauth2.Config{}
		if q.Get("token_endpoint") != "" {
			oauth2.ClientID = q.Get("client_id")
			oauth2.ClientSecret = q.Get("client_secret")
			oauth2.Scopes = []string{q.Get("scope")}
			oauth2.Endpoint.TokenURL = q.Get("token_endpoint")
		}
		password, _ := uri.User.Password()
		bearer := lib.OAuthBearer{
			OAuth2:  oauth2,
			Enabled: true,
		}
		if bearer.OAuth2.Endpoint.TokenURL != "" {
			token, err := bearer.ExchangeRefreshToken(password)
			if err != nil {
				return nil, err
			}
			password = token.AccessToken
		}
		saslClient = sasl.NewOAuthBearerClient(&sasl.OAuthBearerOptions{
			Username: uri.User.Username(),
			Token:    password,
		})
	case "xoauth2":
		q := uri.Query()
		oauth2 := &oauth2.Config{}
		if q.Get("token_endpoint") != "" {
			oauth2.ClientID = q.Get("client_id")
			oauth2.ClientSecret = q.Get("client_secret")
			oauth2.Scopes = []string{q.Get("scope")}
			oauth2.Endpoint.TokenURL = q.Get("token_endpoint")
		}
		password, _ := uri.User.Password()
		bearer := lib.Xoauth2{
			OAuth2:  oauth2,
			Enabled: true,
		}
		if bearer.OAuth2.Endpoint.TokenURL != "" {
			token, err := bearer.ExchangeRefreshToken(password)
			if err != nil {
				return nil, err
			}
			password = token.AccessToken
		}
		saslClient = lib.NewXoauth2Client(uri.User.Username(), password)
	default:
		return nil, fmt.Errorf("Unsupported auth mechanism %s", auth)
	}
	return saslClient, nil
}

type smtpSender struct {
	ctx  sendCtx
	conn *smtp.Client
	w    io.WriteCloser
}

func (s *smtpSender) Write(p []byte) (int, error) {
	return s.w.Write(p)
}

func (s *smtpSender) Close() error {
	we := s.w.Close()
	ce := s.conn.Close()
	if we != nil {
		return we
	}
	return ce
}

func newSmtpSender(ctx sendCtx) (io.WriteCloser, error) {
	var (
		err  error
		conn *smtp.Client
	)
	switch ctx.scheme {
	case "smtp":
		conn, err = connectSmtp(ctx.starttls, ctx.uri.Host, ctx.domain)
	case "smtps":
		conn, err = connectSmtps(ctx.uri.Host)
	default:
		return nil, fmt.Errorf("not an smtp protocol %s", ctx.scheme)
	}

	if err != nil {
		return nil, errors.Wrap(err, "Connection failed")
	}

	saslclient, err := newSaslClient(ctx.auth, ctx.uri)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if saslclient != nil {
		if err := conn.Auth(saslclient); err != nil {
			conn.Close()
			return nil, errors.Wrap(err, "conn.Auth")
		}
	}
	s := &smtpSender{
		ctx:  ctx,
		conn: conn,
	}
	if err := s.conn.Mail(s.ctx.from.Address, nil); err != nil {
		conn.Close()
		return nil, errors.Wrap(err, "conn.Mail")
	}
	for _, rcpt := range s.ctx.rcpts {
		if err := s.conn.Rcpt(rcpt.Address); err != nil {
			conn.Close()
			return nil, errors.Wrap(err, "conn.Rcpt")
		}
	}
	s.w, err = s.conn.Data()
	if err != nil {
		conn.Close()
		return nil, errors.Wrap(err, "conn.Data")
	}
	return s.w, nil
}

func connectSmtp(starttls bool, host string, domain string) (*smtp.Client, error) {
	serverName := host
	if !strings.ContainsRune(host, ':') {
		host += ":587" // Default to submission port
	} else {
		serverName = host[:strings.IndexRune(host, ':')]
	}
	conn, err := smtp.Dial(host)
	if err != nil {
		return nil, errors.Wrap(err, "smtp.Dial")
	}
	if domain != "" {
		err := conn.Hello(domain)
		if err != nil {
			return nil, errors.Wrap(err, "Hello")
		}
	}
	if sup, _ := conn.Extension("STARTTLS"); sup {
		if !starttls {
			err := errors.New("STARTTLS is supported by this server, " +
				"but not set in accounts.conf. " +
				"Add smtp-starttls=yes")
			conn.Close()
			return nil, err
		}
		if err = conn.StartTLS(&tls.Config{
			ServerName: serverName,
		}); err != nil {
			conn.Close()
			return nil, errors.Wrap(err, "StartTLS")
		}
	} else if starttls {
		err := errors.New("STARTTLS requested, but not supported " +
			"by this SMTP server. Is someone tampering with your " +
			"connection?")
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func connectSmtps(host string) (*smtp.Client, error) {
	serverName := host
	if !strings.ContainsRune(host, ':') {
		host += ":465" // Default to smtps port
	} else {
		serverName = host[:strings.IndexRune(host, ':')]
	}
	conn, err := smtp.DialTLS(host, &tls.Config{
		ServerName: serverName,
	})
	if err != nil {
		return nil, errors.Wrap(err, "smtp.DialTLS")
	}
	return conn, nil
}

func copyToSent(worker *types.Worker, dest string,
	n int, msg io.Reader,
) <-chan error {
	errCh := make(chan error)
	worker.PostAction(&types.AppendMessage{
		Destination: dest,
		Flags:       models.SeenFlag,
		Date:        time.Now(),
		Reader:      msg,
		Length:      n,
	}, func(msg types.WorkerMessage) {
		switch msg := msg.(type) {
		case *types.Done:
			errCh <- nil
		case *types.Error:
			errCh <- msg.Error
		}
	})
	return errCh
}
