// Tuigram is a keyboard-driven Telegram client for the terminal.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/config"
	"github.com/paramon-tech/tuigram/internal/core"
	"github.com/paramon-tech/tuigram/internal/demo"
	"github.com/paramon-tech/tuigram/internal/storage"
	"github.com/paramon-tech/tuigram/internal/telegram"
	"github.com/paramon-tech/tuigram/internal/tui"
	"golang.org/x/term"
)

var version = "dev"
var commit = "unknown"
var buildDate = "unknown"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintln(os.Stderr, "tuigram:", tui.Sanitize(err.Error()))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, in *os.File, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("tuigram", flag.ContinueOnError)
	flags.SetOutput(errOut)
	configPath := flags.String("config", "", "path to private JSON configuration")
	demoMode := flags.Bool("demo", false, "use an isolated, account-free demo")
	snapshot := flags.Bool("snapshot", false, "render the demo once without a terminal (requires --demo)")
	qr := flags.Bool("qr", false, "sign in by scanning a QR code")
	theme := flags.String("theme", "", "theme: midnight, light, or dracula")
	showVersion := flags.Bool("version", false, "print version and exit")
	flags.Usage = func() {
		fmt.Fprintln(errOut, "Usage: tuigram [flags] [config init | cache stats | cache clear]\n\nA keyboard-driven Telegram client. Start with --demo; press ? for keys.\nFlags must precede commands.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Fprintf(out, "tuigram %s (%s, %s)\n", version, commit, buildDate)
		return nil
	}
	if *snapshot && !*demoMode {
		return errors.New("--snapshot requires --demo")
	}
	command := strings.Join(flags.Args(), " ")
	if command != "" && command != "config init" && command != "cache stats" && command != "cache clear" {
		return fmt.Errorf("unknown command %q; use --help", command)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *theme != "" {
		cfg.Theme = *theme
		if err := cfg.Validate(); err != nil {
			return err
		}
	}
	if command == "config init" {
		path := *configPath
		if path == "" {
			path, err = config.DefaultPath()
			if err != nil {
				return err
			}
		}
		if _, err := os.Lstat(path); err == nil {
			return errors.New("configuration already exists; edit it to change settings")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// API credentials stay in the environment unless deliberately configured.
		cfg.AppID = 0
		cfg.AppHash = ""
		if err := config.Save(path, cfg); err != nil {
			return err
		}
		fmt.Fprintln(out, "Created", path)
		return nil
	}
	if command != "" {
		cache, err := storage.NewCache(cfg.CacheDir, cfg.CacheMaxBytes, time.Duration(cfg.CacheTTLHours)*time.Hour)
		if err != nil {
			return err
		}
		if command == "cache clear" {
			if err := cache.Clear(); err != nil {
				return err
			}
			fmt.Fprintln(out, "Media cache cleared; your encrypted session is unchanged.")
			return nil
		}
		n, size, err := cache.Stats()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%d files, %d bytes (limit %d bytes, TTL %dh)\n", n, size, cfg.CacheMaxBytes, cfg.CacheTTLHours)
		return nil
	}
	opts := tui.Options{Theme: cfg.Theme, PollInterval: time.Duration(cfg.PollSeconds) * time.Second}
	if *demoMode {
		client := demo.New()
		if *snapshot {
			view, err := tui.Snapshot(ctx, client, opts, 100, 28)
			if err != nil {
				return err
			}
			fmt.Fprintln(out, view)
			return nil
		}
		return runTUI(ctx, client, opts, in, out)
	}
	if cfg.AppID == 0 || cfg.AppHash == "" {
		return errors.New("set TUIGRAM_API_ID and TUIGRAM_API_HASH from my.telegram.org, or try tuigram --demo")
	}
	if !term.IsTerminal(int(in.Fd())) {
		return errors.New("interactive login requires a terminal; use --demo --snapshot for a headless preview")
	}
	cache, err := storage.NewCache(cfg.CacheDir, cfg.CacheMaxBytes, time.Duration(cfg.CacheTTLHours)*time.Hour)
	if err != nil {
		return err
	}
	opts.Cache = cache
	sessionPath := filepath.Join(cfg.StateDir, "session.enc")
	release, err := storage.AcquireLock(filepath.Join(cfg.StateDir, "session.lock"))
	if err != nil {
		return err
	}
	defer release()
	passphrase, err := readPassphrase(ctx, in, errOut, sessionPath)
	if err != nil {
		return err
	}
	session, err := storage.NewSession(sessionPath, passphrase)
	clear(passphrase)
	if err != nil {
		return err
	}
	defer session.Close()
	return telegram.Run(ctx, telegram.Options{AppID: cfg.AppID, AppHash: cfg.AppHash, SessionStorage: session, QR: *qr, Input: in, Output: errOut}, func(clientCtx context.Context, client core.Client) error {
		return runTUI(clientCtx, client, opts, in, out)
	})
}

func runTUI(ctx context.Context, client core.Client, opts tui.Options, in *os.File, out io.Writer) error {
	if !term.IsTerminal(int(in.Fd())) {
		return errors.New("TUI requires a terminal; use --demo --snapshot for a headless preview")
	}
	p := tea.NewProgram(tui.New(ctx, client, opts), tea.WithContext(ctx), tea.WithAltScreen(), tea.WithInput(in), tea.WithOutput(out))
	_, err := p.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func readPassphrase(ctx context.Context, in *os.File, out io.Writer, sessionPath string) ([]byte, error) {
	if value := os.Getenv("TUIGRAM_SESSION_PASSPHRASE"); value != "" {
		return []byte(value), nil
	}
	_, statErr := os.Lstat(sessionPath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	fmt.Fprint(out, "Local session passphrase (12+ characters; encrypts your login): ")
	passphrase, err := readSecret(ctx, in, out)
	fmt.Fprintln(out)
	if err != nil {
		clear(passphrase)
		return nil, err
	}
	if errors.Is(statErr, os.ErrNotExist) {
		fmt.Fprint(out, "Repeat local passphrase: ")
		again, err := readSecret(ctx, in, out)
		fmt.Fprintln(out)
		defer clear(again)
		if err != nil {
			clear(passphrase)
			return nil, err
		}
		if !bytes.Equal(passphrase, again) {
			clear(passphrase)
			return nil, errors.New("local passphrases do not match")
		}
	}
	return passphrase, nil
}

// readSecret uses the terminal's line editor so Ctrl+C works while echo is off.
// Cancellation also restores the terminal immediately on SIGTERM.
func readSecret(ctx context.Context, in *os.File, out io.Writer) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return nil, err
	}
	defer term.Restore(int(in.Fd()), state)
	terminal := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{io.LimitReader(in, 4097), out}, "")
	type result struct {
		value []byte
		err   error
	}
	done := make(chan result)
	go func() {
		value, err := terminal.ReadPassword("")
		data := []byte(value)
		select {
		case done <- result{data, err}:
		case <-ctx.Done():
			clear(data)
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-done:
		if res.err != nil {
			clear(res.value)
			if errors.Is(res.err, io.EOF) {
				return nil, context.Canceled
			}
			return nil, res.err
		}
		if len(res.value) > 4096 {
			clear(res.value)
			return nil, errors.New("passphrase exceeds 4096 bytes")
		}
		return res.value, nil
	}
}
