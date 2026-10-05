package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/config"
)

type settingsState struct {
	values  []string
	index   int
	editing bool
}
type settingsSavedMsg struct {
	preferences  config.Preferences
	audioChanged bool
	err          error
}

func (m Model) preferences() config.Preferences {
	p := m.opts.Preferences
	p.Theme, p.DownloadDir, p.MarkRead = m.opts.Theme, m.opts.DownloadDir, !m.opts.DisableAutoRead
	p.PollSeconds = int(m.opts.PollInterval / time.Second)
	if p.PollSeconds < 2 {
		p.PollSeconds = 15
	}
	return p
}

func (m *Model) openSettings() {
	p := m.preferences()
	m.settings = settingsState{values: []string{p.Theme, p.DownloadDir, strconv.Itoa(p.PollSeconds), strconv.FormatBool(p.MarkRead), p.CallInputFormat, p.CallInputDevice}}
	for _, s := range config.Shortcuts() {
		m.settings.values = append(m.settings.values, p.Key(s.Action))
	}
	m.mode, m.failure = settingsPanel, ""
}

func (m Model) updateSettingsKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	m.settings.values = append([]string(nil), m.settings.values...)
	s := &m.settings
	switch key.String() {
	case "ctrl+s":
		p, err := m.settingsPreferences()
		if err != nil {
			m.setFailure("settings", err.Error())
			return m, nil
		}
		m.busy = true
		m.status = "Saving settings…"
		save := m.opts.SavePreferences
		changed := p.CallInputDevice != m.opts.Preferences.CallInputDevice || p.CallInputFormat != m.opts.Preferences.CallInputFormat
		return m, func() tea.Msg {
			var err error
			if save != nil {
				err = save(p)
			}
			return settingsSavedMsg{p, changed, err}
		}
	case "tab", "down":
		s.index = (s.index + 1) % len(s.values)
		s.editing = false
	case "shift+tab", "up":
		s.index = (s.index + len(s.values) - 1) % len(s.values)
		s.editing = false
	case "enter":
		if s.index == 0 {
			themes := []string{"midnight", "light", "dracula"}
			for i, v := range themes {
				if s.values[0] == v {
					s.values[0] = themes[(i+1)%len(themes)]
					break
				}
			}
		} else if s.index == 3 {
			s.values[3] = strconv.FormatBool(s.values[3] != "true")
		} else {
			s.editing = !s.editing
		}
	case "ctrl+u":
		if s.index != 0 && s.index != 3 {
			s.values[s.index] = ""
			s.editing = true
		}
	case "backspace", "ctrl+h":
		if s.editing {
			r := []rune(s.values[s.index])
			if len(r) > 0 {
				s.values[s.index] = string(r[:len(r)-1])
			}
		}
	default:
		if s.editing && (key.Type == tea.KeyRunes || key.Type == tea.KeySpace) && !key.Alt {
			value := strings.ReplaceAll(Sanitize(string(key.Runes)), "\n", "")
			if key.Type == tea.KeySpace {
				value = " "
			}
			limit := 4096
			if s.index >= 6 {
				limit = 1
			}
			if s.index == 5 {
				limit = 256
			}
			r := []rune(value)
			room := max(0, limit-len([]rune(s.values[s.index])))
			s.values[s.index] += string(r[:min(len(r), room)])
		}
	}
	return m, nil
}

func (m Model) settingsPreferences() (config.Preferences, error) {
	v := m.settings.values
	p := m.preferences()
	p.Theme, p.DownloadDir, p.CallInputFormat, p.CallInputDevice = v[0], v[1], strings.TrimSpace(v[4]), v[5]
	if strings.HasPrefix(p.DownloadDir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p, err
		}
		p.DownloadDir = filepath.Join(home, p.DownloadDir[2:])
	}
	p.PollSeconds, _ = strconv.Atoi(v[2])
	p.MarkRead = v[3] == "true"
	p.KeyBindings = make(map[string]string)
	for i, s := range config.Shortcuts() {
		if v[6+i] != s.Key {
			p.KeyBindings[s.Action] = v[6+i]
		}
	}
	// An empty edited shortcut is a mistake, not a request to restore defaults.
	for i, s := range config.Shortcuts() {
		if v[6+i] == "" {
			return p, fmt.Errorf("enter a shortcut for %s", s.Label)
		}
	}
	return p, p.Validate()
}

func (m Model) updateSettingsSaved(msg settingsSavedMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	if msg.err != nil {
		m.setFailure("settings", msg.err.Error())
		return m, nil
	}
	p := msg.preferences
	reschedule := m.opts.PollInterval >= 0 && m.opts.PollInterval != time.Duration(p.PollSeconds)*time.Second
	m.opts.Preferences = p
	m.opts.Theme, m.opts.DownloadDir = p.Theme, p.DownloadDir
	if m.opts.PollInterval >= 0 {
		m.opts.PollInterval = time.Duration(p.PollSeconds) * time.Second
	}
	m.opts.DisableAutoRead = !p.MarkRead
	m.failure = ""
	m.status = "Settings saved"
	if m.opts.SavePreferences == nil {
		m.status = "Settings applied for this session"
	}
	if msg.audioChanged {
		m.status += " · restart to apply microphone settings"
	}
	if m.mode == callPanel && m.callReturnMode == settingsPanel {
		m.callReturnMode = normal
	} else if m.mode == settingsPanel {
		m.mode = normal
	}
	var tick tea.Cmd
	if reschedule {
		m.pollGeneration++
		tick = m.tickCmd()
	}
	read := m.markVisibleRead()
	return m, tea.Batch(tick, read)
}

func (m Model) settingsText() string {
	labels := []string{"Theme", "Download directory", "Refresh seconds", "Mark viewed messages read", "Microphone backend (restart)", "Microphone device (restart)"}
	for _, s := range config.Shortcuts() {
		labels = append(labels, "Key: "+s.Label)
	}
	available := max(1, m.height-12)
	var lines []string
	selectedEnd := 0
	for i := 0; i < len(labels); i++ {
		prefix := "  "
		if i == m.settings.index {
			prefix = "› "
		}
		v := m.settings.values[i]
		if v == "" {
			v = "(default)"
		}
		if i == m.settings.index && m.settings.editing {
			v = Sanitize(m.settings.values[i]) + "▌"
		}
		lines = append(lines, wrapText(prefix+labels[i]+": "+Sanitize(v), max(1, m.width-4))...)
		if i == m.settings.index {
			selectedEnd = len(lines) - 1
		}
	}
	start := max(0, selectedEnd-available+1)
	lines = lines[start:min(len(lines), start+available)]
	hint := "Enter edits · Ctrl+u clears the field · Ctrl+s saves all"
	switch m.settings.index {
	case 0, 3:
		hint = "Enter changes this option · Ctrl+s saves all"
	case 1:
		hint = "Blank uses ~/Downloads/tuigram; an existing folder must be private (0700)."
	case 4:
		hint = "Blank selects the platform default; avfoundation / pulse / alsa / oss / sndio."
	case 5:
		hint = "Use tuigram audio devices to list microphones. Speakers follow the system output."
	}
	return strings.Join(lines, "\n") + "\n\n" + hint
}

// Translate only normal-mode action keys; typing and fixed navigation keys are
// unaffected. A remapped action's old key is disabled unless reassigned.
func (m Model) actionKey(key string) string {
	p := m.preferences()
	for _, s := range config.Shortcuts() {
		if key == p.Key(s.Action) {
			return s.Key
		}
	}
	for _, s := range config.Shortcuts() {
		if key == s.Key {
			return ""
		}
	}
	return key
}
