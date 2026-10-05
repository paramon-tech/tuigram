package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/config"
)

func TestSettingsSaveIsDeferredAndAppliesOnlyAfterSuccess(t *testing.T) {
	m, _ := fixture()
	writes := 0
	m.opts.SavePreferences = func(p config.Preferences) error {
		writes++
		if p.Theme != "light" {
			t.Fatal("wrong preferences")
		}
		return errors.New("disk full")
	}
	m, _ = press(m, ",")
	m.settings.values[0] = "light"
	m.settings.values[2] = "8"
	m.settings.values[3] = "false"
	m, cmd := press(m, "ctrl+s")
	if writes != 0 || cmd == nil || !m.busy {
		t.Fatal("save not deferred")
	}
	m = complete(m, cmd)
	if writes != 1 || m.opts.Theme != "midnight" || m.mode != settingsPanel || m.failure != "disk full" {
		t.Fatal("failed save altered active preferences")
	}
	m.opts.SavePreferences = func(config.Preferences) error { return nil }
	m.opts.PollInterval = time.Second * 5
	m, cmd = press(m, "ctrl+s")
	m = complete(m, cmd)
	if m.opts.Theme != "light" || !m.opts.DisableAutoRead || m.opts.PollInterval != 8*time.Second || m.mode != normal {
		t.Fatal("saved preferences not applied")
	}
}

func TestSettingsPollChangeInvalidatesOldTimer(t *testing.T) {
	m, _ := fixture()
	m.opts.PollInterval = 24 * time.Hour
	p := m.preferences()
	p.PollSeconds = 2
	oldGeneration := m.pollGeneration
	next, cmd := m.updateSettingsSaved(settingsSavedMsg{preferences: p})
	m = next.(Model)
	if cmd == nil || m.pollGeneration == oldGeneration || m.opts.PollInterval != 2*time.Second {
		t.Fatal("poll interval did not schedule a replacement timer")
	}
	request := m.dialogsRequest
	next, cmd = m.Update(pollTickMsg{generation: oldGeneration, time: time.Now()})
	m = next.(Model)
	if cmd != nil || m.dialogsRequest != request {
		t.Fatal("obsolete timer restarted polling")
	}
}

func TestLongSettingsFieldKeepsSelectedCursorVisible(t *testing.T) {
	m, _ := fixture()
	m.width, m.height = 40, 24
	m.openSettings()
	m.settings.values[1] = "/" + strings.Repeat("long path/", 100)
	m.settings.index = 1
	m.settings.editing = true
	if !strings.Contains(m.View(), "▌") {
		t.Fatal("long selected settings path hides cursor")
	}
	m.settings.index = 5
	m.settings.values[5] = "USB microphone"
	m.settings.editing = true
	if !strings.Contains(m.View(), "microphone▌") {
		t.Fatalf("earlier long settings field hides selected device:\n%s", m.View())
	}
}

func TestSettingsRemapsActionsButNeverComposerTyping(t *testing.T) {
	m, _ := fixture()
	m.opts.Preferences.KeyBindings = map[string]string{"settings": ";", "attach": ",", "compose": "z"}
	m, _ = press(m, "i")
	if m.mode != normal {
		t.Fatal("old binding remains active")
	}
	m, _ = press(m, ";")
	if m.mode != settingsPanel {
		t.Fatal("settings remap failed")
	}
	m, _ = press(m, "esc")
	m, _ = press(m, "z")
	m, _ = press(m, ";")
	if m.mode != compose || m.input != ";" {
		t.Fatal("key binding intercepted composer text")
	}
}

func TestSettingsCancelAndShortcutValidation(t *testing.T) {
	m, _ := fixture()
	m.openSettings()
	m.settings.values[0] = "light"
	m, _ = press(m, "esc")
	if m.opts.Theme != "midnight" {
		t.Fatal("cancel changed settings")
	}
	m.openSettings()
	m.settings.values[6] = "q"
	m, cmd := press(m, "ctrl+s")
	if cmd != nil || m.failure == "" || m.busy {
		t.Fatal("invalid shortcut saved")
	}
	m.settings.index = 1
	next, _ := m.updateSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.updateSettingsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/tmp/a path")})
	m = next.(Model)
	if m.settings.values[1] != "/tmp/a path" {
		t.Fatal("path with spaces not editable")
	}
}
