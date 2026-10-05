package tui

import (
	"strings"
	"testing"

	"github.com/paramon-tech/tuigram/internal/core"
)

func TestReactionPickerKeepsCapturedMessageAndAllowsRemoval(t *testing.T) {
	m, fake := fixture()
	m, cmd := press(m, "r")
	if cmd != nil || m.mode != reactionPicker || len(fake.calls) != 0 {
		t.Fatal("opening picker sent a reaction")
	}
	m, _ = press(m, "down")
	m.chatIndex = 1 // Selection changes must never redirect a queued reaction.
	m, cmd = press(m, "enter")
	if !m.busy || cmd == nil || m.mode != normal {
		t.Fatal("reaction submit did not become busy")
	}
	m = complete(m, cmd)
	if fake.calls[len(fake.calls)-1] != "react:one:2:❤" {
		t.Fatalf("wrong reaction target: %v", fake.calls)
	}
	m.chatIndex = 0
	m, _ = press(m, "r")
	m, _ = press(m, "0")
	m, cmd = press(m, "enter")
	m = complete(m, cmd)
	if fake.calls[len(fake.calls)-1] != "react:one:2:" {
		t.Fatalf("remove should send empty reaction: %v", fake.calls)
	}
}

func TestReactionPickerSelectsOwnReactionAndEscDoesNotSend(t *testing.T) {
	m, fake := fixture()
	m.messages[m.messageIndex].Reactions = []core.Reaction{{Emoji: "🔥", Count: 3, Mine: true}}
	m, _ = press(m, "r")
	if reactionChoices[m.reaction.index] != "🔥" {
		t.Fatal("picker did not select own reaction")
	}
	m, cmd := press(m, "esc")
	if cmd != nil || len(fake.calls) != 0 || m.mode != normal {
		t.Fatal("canceling picker sent a reaction")
	}
}

func TestReactionPickerSelectedRemovalVisibleAtSmallHeight(t *testing.T) {
	for _, height := range []int{12, 16, 24} {
		m, _ := fixture()
		m.width, m.height = 40, height
		m, _ = press(m, "r")
		m, _ = press(m, "0")
		if !strings.Contains(m.View(), "> Remove my reaction") {
			t.Fatalf("selected removal clipped at height %d: %s", height, m.View())
		}
	}
}
