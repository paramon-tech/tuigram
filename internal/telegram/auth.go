// Package telegram connects the transport-independent client to Telegram's
// MTProto API. Peers and access hashes live only in memory; callers own session
// encryption and must supply a secure session.Storage.
package telegram

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gotd/td/session"
	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/mdp/qrterminal/v3"
	"github.com/paramon-tech/tuigram/internal/core"
	"golang.org/x/term"
)

// Options contains API credentials and the encrypted session store. Credentials
// come from my.telegram.org. Input and Output default to stdin and stderr.
type Options struct {
	AppID          int
	AppHash        string
	SessionStorage session.Storage
	QR             bool
	Input          io.Reader
	Output         io.Writer
}

// Run connects, authenticates an existing Telegram account, and keeps the
// connection alive until fn returns. QR codes renew automatically. Signing up
// for a new Telegram account must be done in an official Telegram application.
func Run(ctx context.Context, opts Options, fn func(context.Context, core.Client) error) error {
	if opts.AppID <= 0 || strings.TrimSpace(opts.AppHash) == "" {
		return errors.New("Telegram API ID and API hash are required")
	}
	if opts.SessionStorage == nil {
		return errors.New("encrypted Telegram session storage is required")
	}
	if fn == nil {
		return errors.New("Telegram client callback is required")
	}
	if opts.Input == nil {
		opts.Input = os.Stdin
	}
	if opts.Output == nil {
		opts.Output = os.Stderr
	}
	prompt := newPrompter(opts.Input, opts.Output)
	dispatcher := tg.NewUpdateDispatcher()
	loggedIn := qrlogin.OnLoginToken(&dispatcher)
	client := gotd.NewClient(opts.AppID, opts.AppHash, gotd.Options{
		SessionStorage: opts.SessionStorage,
		UpdateHandler:  dispatcher,
	})
	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("check Telegram authorization: %w", err)
		}
		if !status.Authorized {
			if opts.QR {
				_, err = client.QR().Auth(ctx, loggedIn, func(ctx context.Context, token qrlogin.Token) error {
					if err := ctx.Err(); err != nil {
						return err
					}
					fmt.Fprintln(opts.Output, "Scan in Telegram: Settings > Devices > Link Desktop Device")
					qrterminal.GenerateHalfBlock(token.URL(), qrterminal.L, opts.Output)
					return nil
				})
				if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
					password, passwordErr := prompt.Password(ctx)
					if passwordErr != nil {
						return passwordErr
					}
					_, err = client.Auth().Password(ctx, password)
				}
			} else {
				err = client.Auth().IfNecessary(ctx, auth.NewFlow(prompt, auth.SendCodeOptions{}))
			}
			if err != nil {
				return fmt.Errorf("Telegram authentication: %w", err)
			}
		}
		self, err := client.Self(ctx)
		if err != nil {
			return fmt.Errorf("identify Telegram account: %w", err)
		}
		if self.ID <= 0 {
			return errors.New("Telegram returned an invalid account identity")
		}
		backend := newClient(client)
		backend.accountID = self.ID
		return fn(ctx, backend)
	})
}

type prompter struct {
	input  io.Reader
	reader *bufio.Reader
	output io.Writer
}

const maxPromptBytes = 4096

func newPrompter(input io.Reader, output io.Writer) *prompter {
	return &prompter{input: input, reader: bufio.NewReaderSize(input, maxPromptBytes), output: output}
}

func (p *prompter) line(ctx context.Context, label string, secret bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := fmt.Fprint(p.output, label); err != nil {
		return "", err
	}
	read := func() (string, error) {
		data, prefix, err := p.reader.ReadLine()
		if secret {
			defer clear(data)
		}
		if prefix {
			return "", errors.New("authentication input exceeds 4096 bytes")
		}
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	restore := func() {}
	if input, ok := p.input.(*os.File); secret && ok && term.IsTerminal(int(input.Fd())) {
		fd := int(input.Fd())
		state, err := term.MakeRaw(fd)
		if err != nil {
			return "", err
		}
		restore = func() { _ = term.Restore(fd, state) }
		defer restore()
		terminal := term.NewTerminal(struct {
			io.Reader
			io.Writer
		}{io.LimitReader(input, maxPromptBytes+1), p.output}, "")
		read = func() (string, error) {
			value, err := terminal.ReadPassword("")
			if err != nil {
				return "", err
			}
			if len(value) > maxPromptBytes {
				return "", errors.New("authentication input exceeds 4096 bytes")
			}
			return value, nil
		}
	}
	// Arbitrary io.Readers cannot be interrupted. Keep at most the current read
	// alive until its input closes, while allowing connection shutdown and
	// restoring terminal echo immediately when the application is cancelled.
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	go func() { value, err := read(); done <- result{value, err} }()
	select {
	case <-ctx.Done():
		restore()
		return "", ctx.Err()
	case value := <-done:
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return value.value, value.err
	}
}

func (p *prompter) Phone(ctx context.Context) (string, error) {
	value, err := p.line(ctx, "Phone number (including country code): ", false)
	return strings.TrimSpace(value), err
}

func (p *prompter) Password(ctx context.Context) (string, error) {
	return p.line(ctx, "Telegram two-step verification password: ", true)
}

func (p *prompter) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	value, err := p.line(ctx, "Telegram login code: ", true)
	return strings.TrimSpace(value), err
}

func (p *prompter) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return errors.New("create the account using an official Telegram app before signing in")
}

func (p *prompter) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("create the account using an official Telegram app before signing in")
}
