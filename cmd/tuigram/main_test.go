package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cliEnvironment(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	t.Setenv("TUIGRAM_API_ID", "")
	t.Setenv("TUIGRAM_API_HASH", "")
	t.Setenv("TUIGRAM_SESSION_PASSPHRASE", "")
	return root
}

func TestDemoSnapshot(t *testing.T) {
	root := cliEnvironment(t)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--demo", "--snapshot"}, os.Stdin, &out, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tuigram", "Saved Messages", "Welcome to tuigram"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("demo wrote local state: %v %v", entries, err)
	}
}

func TestConfigAndCacheCommands(t *testing.T) {
	cliEnvironment(t)
	var out bytes.Buffer
	ctx := context.Background()
	for _, args := range [][]string{{"config", "init"}, {"cache", "stats"}, {"cache", "clear"}} {
		if err := run(ctx, args, os.Stdin, &out, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if err := run(ctx, []string{"config", "init"}, os.Stdin, &out, &out); err == nil {
		t.Fatal("config overwritten")
	}
	if err := run(ctx, nil, os.Stdin, &out, &out); err == nil || !strings.Contains(err.Error(), "TUIGRAM_API_ID") {
		t.Fatalf("missing credentials: %v", err)
	}
}

func TestCLIValidation(t *testing.T) {
	cliEnvironment(t)
	for _, args := range [][]string{{"--snapshot"}, {"--theme", "unknown", "--demo", "--snapshot"}, {"unknown"}} {
		var out bytes.Buffer
		if err := run(context.Background(), args, os.Stdin, &out, &out); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	for _, args := range [][]string{{"--help"}, {"--version"}} {
		var out bytes.Buffer
		if err := run(context.Background(), args, os.Stdin, &out, &out); err != nil {
			t.Errorf("%v: %v", args, err)
		}
		if out.Len() == 0 {
			t.Error("missing output")
		}
	}
}

func TestAudioDiagnosticsDoNotRequireTelegramCredentials(t *testing.T) {
	cliEnvironment(t)
	t.Setenv("PATH", t.TempDir())
	for _, command := range []string{"check", "devices", "speaker-test"} {
		var out bytes.Buffer
		err := run(context.Background(), []string{"audio", command}, os.Stdin, &out, &out)
		if err == nil || !strings.Contains(err.Error(), "FFmpeg") || strings.Contains(err.Error(), "TUIGRAM_API_ID") {
			t.Fatalf("audio %s did not reach offline diagnostics: %v", command, err)
		}
	}
}
