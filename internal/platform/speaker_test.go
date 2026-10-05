package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSpeakerTestSurfacesOutputFailure(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"fail", "failzero"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TUIGRAM_TEST_SPEAKER_HELPER", mode)
			err := runSpeakerTest(context.Background(), executable, []string{"-test.run=^TestSpeakerHelperProcess$"})
			if err == nil || !strings.Contains(err.Error(), "audio device unavailable") {
				t.Fatalf("speaker failure lost its diagnostic: %v", err)
			}
		})
	}
}

func TestSpeakerTestCancellationStopsPlayer(t *testing.T) {
	t.Setenv("TUIGRAM_TEST_SPEAKER_HELPER", "wait")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = runSpeakerTest(ctx, executable, []string{"-test.run=^TestSpeakerHelperProcess$"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("speaker test ignored cancellation: %v", err)
	}
}

func TestSpeakerHelperProcess(t *testing.T) {
	switch os.Getenv("TUIGRAM_TEST_SPEAKER_HELPER") {
	case "fail":
		fmt.Fprintln(os.Stderr, "audio device unavailable")
		os.Exit(1)
	case "failzero":
		fmt.Fprintln(os.Stderr, "audio device unavailable")
		os.Exit(0)
	case "wait":
		time.Sleep(time.Minute)
		os.Exit(1)
	}
}
