package tui_test

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
	"github.com/paramon-tech/tuigram/internal/demo"
	"github.com/paramon-tech/tuigram/internal/tui"
)

// drain drives the same Update/command boundary as Bubble Tea. The model uses
// PollInterval=-1 so this helper never executes a timer or needs a terminal.
func drain(t *testing.T, model tea.Model, command tea.Cmd) tea.Model {
	t.Helper()
	commands := []tea.Cmd{command}
	for steps := 0; len(commands) > 0; steps++ {
		if steps > 100 {
			t.Fatal("command chain did not settle")
		}
		command, commands = commands[0], commands[1:]
		if command == nil {
			continue
		}
		message := command()
		if batch, ok := message.(tea.BatchMsg); ok {
			commands = append(commands, batch...)
			continue
		}
		model, command = model.Update(message)
		commands = append(commands, command)
	}
	return model
}

func dispatch(t *testing.T, model tea.Model, message tea.Msg) tea.Model {
	t.Helper()
	model, command := model.Update(message)
	return drain(t, model, command)
}

func key(t *testing.T, model tea.Model, name string) tea.Model {
	t.Helper()
	keys := map[string]tea.KeyType{"enter": tea.KeyEnter, "esc": tea.KeyEsc, "ctrl+s": tea.KeyCtrlS, "tab": tea.KeyTab}
	if keyType, ok := keys[name]; ok {
		return dispatch(t, model, tea.KeyMsg{Type: keyType})
	}
	return dispatch(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)})
}

func paste(t *testing.T, model tea.Model, text string) tea.Model {
	t.Helper()
	return dispatch(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: true})
}

func history(t *testing.T, client core.Client, chatID string) []core.Message {
	t.Helper()
	messages, err := client.History(context.Background(), core.Chat{ID: chatID}, "")
	if err != nil {
		t.Fatal(err)
	}
	return messages
}

func TestDemoFunctionalConversationJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := demo.New()
	var model tea.Model = tui.New(ctx, client, tui.Options{PollInterval: -1})
	model = drain(t, model, model.Init())
	model = dispatch(t, model, tea.WindowSizeMsg{Width: 120, Height: 32})
	if !strings.Contains(model.View(), "Saved Messages") {
		t.Fatal("initial dialogs/history not rendered")
	}

	const message = "Integration handshake 👋 https://example.com"
	model = key(t, model, "i")
	model = paste(t, model, message)
	model = key(t, model, "ctrl+s")
	saved := history(t, client, "saved")
	if saved[len(saved)-1].Text != message {
		t.Fatal("composer did not send to selected conversation")
	}
	model = key(t, model, "/")
	model = paste(t, model, "Integration handshake")
	model = key(t, model, "enter")
	view := model.View()
	if !strings.Contains(view, message) || strings.Contains(view, "Welcome to tuigram") {
		t.Fatal("message search did not filter rendered history")
	}
	model = key(t, model, "r")
	model = key(t, model, "enter")
	saved = history(t, client, "saved")
	if reactions := saved[len(saved)-1].Reactions; len(reactions) != 1 || reactions[0].Emoji != "👍" || reactions[0].Count != 1 {
		t.Fatalf("selected search result did not receive reaction: %v", reactions)
	}
	model = key(t, model, "f")
	model = key(t, model, "j")
	model = key(t, model, "enter")
	builders := history(t, client, "builders")
	forwarded := builders[len(builders)-1]
	if !forwarded.Forwarded || forwarded.ChatID != "builders" || forwarded.Text != message {
		t.Fatalf("forwarded message went to wrong destination: %#v", forwarded)
	}
	model = key(t, model, "esc")

	model = key(t, model, "c")
	model = paste(t, model, "@ada")
	model = key(t, model, "enter")
	if !strings.Contains(model.View(), "Ada Lovelace") {
		t.Fatal("public username search missing")
	}
	model = key(t, model, "enter")
	const privateMessage = "A private hello after opening a contact"
	model = key(t, model, "i")
	model = paste(t, model, privateMessage)
	model = key(t, model, "ctrl+s")
	private := history(t, client, "ada")
	if len(private) != 1 || private[0].Text != privateMessage {
		t.Fatalf("contact chat did not receive message: %#v", private)
	}
	// Opening a discovered peer must work before adding it to the address book.
	// Then exercise adding and reopening the existing conversation as well.
	model = key(t, model, "c")
	model = paste(t, model, "@ada")
	model = key(t, model, "enter")
	model = key(t, model, "a")
	if !strings.Contains(model.View(), "Adding contact complete") {
		t.Fatal("contact addition did not complete")
	}
	model = key(t, model, "enter")
	model = key(t, model, "R")
	model = dispatch(t, model, tea.WindowSizeMsg{Width: 50, Height: 18})
	if !strings.Contains(model.View(), "Ada Lovelace") {
		t.Fatal("dialog refresh lost contact selection on narrow terminal")
	}
	model = key(t, model, "y")
	if !strings.Contains(model.View(), "A private hello") {
		t.Fatal("full-message reader lost selected contact message")
	}
	model = key(t, model, "esc")

	// A direct backend insertion represents hostile remote text; rendering it
	// must preserve readable content without executing clipboard or CSI control.
	if err := client.Send(ctx, core.Chat{ID: "ada"}, "safe\x1b]52;c;clipboard-data\a\x1b[2Jtext\u202e"); err != nil {
		t.Fatal(err)
	}
	model = key(t, model, "R")
	view = model.View()
	if !strings.Contains(view, "safetext") || strings.Contains(view, "clipboard-data") || strings.Contains(view, "\x1b[2J") || strings.Contains(view, "\u202e") {
		t.Fatal("hostile remote text reached terminal")
	}
}

func TestDemoFunctionalPhotoPreview(t *testing.T) {
	client := demo.New()
	var model tea.Model = tui.New(context.Background(), client, tui.Options{PollInterval: -1})
	model = drain(t, model, model.Init())
	model = key(t, model, "v")
	if !strings.Contains(model.View(), "▀") {
		t.Fatal("real demo image did not reach decoded terminal preview")
	}
	model = key(t, model, "esc")
	if strings.Contains(model.View(), "Image preview") {
		t.Fatal("image preview did not close")
	}
}

func TestDemoFunctionalManagementJourney(t *testing.T) {
	client := demo.New()
	var model tea.Model = tui.New(context.Background(), client, tui.Options{PollInterval: -1})
	model = drain(t, model, model.Init())
	model = dispatch(t, model, tea.WindowSizeMsg{Width: 110, Height: 30})
	// Browse saved contacts, select a member and create a real demo group.
	model = key(t, model, "N")
	model = dispatch(t, model, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	model = key(t, model, "enter")
	model = paste(t, model, "Release Team")
	model = key(t, model, "enter")
	chats, err := client.Dialogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	group := chats[len(chats)-1]
	if group.Title != "Release Team" || group.Kind != "group" {
		t.Fatalf("group not created: %#v", chats)
	}
	model = key(t, model, "e")
	model = dispatch(t, model, tea.KeyMsg{Type: tea.KeyCtrlU})
	model = paste(t, model, "New Team Name")
	model = key(t, model, "enter")
	chats, _ = client.Dialogs(context.Background())
	if chats[len(chats)-1].Title != "New Team Name" {
		t.Fatal("group not renamed")
	}
	// A destructive action requires y; Enter cannot accidentally confirm it.
	model = key(t, model, "D")
	model = key(t, model, "enter")
	chats, _ = client.Dialogs(context.Background())
	if len(chats) != 4 {
		t.Fatal("Enter deleted group")
	}
	model = key(t, model, "esc")
	model = key(t, model, "D")
	model = key(t, model, "y")
	chats, _ = client.Dialogs(context.Background())
	if len(chats) != 3 {
		t.Fatal("group was not left")
	}
	model = key(t, model, "R")
	if strings.Contains(model.View(), "New Team Name") {
		t.Fatal("refresh resurrected deleted chat")
	}

	// Add by phone, edit a name including a physical space, open and message.
	model = key(t, model, "c")
	model = key(t, model, "enter")
	model = key(t, model, "n")
	model = paste(t, model, "+14155552671")
	model = key(t, model, "enter")
	model = paste(t, model, "Demo Friend")
	model = key(t, model, "enter")
	model = key(t, model, "e")
	model = dispatch(t, model, tea.KeyMsg{Type: tea.KeyCtrlU})
	model = paste(t, model, "Renamed")
	model = dispatch(t, model, tea.KeyMsg{Type: tea.KeySpace})
	model = paste(t, model, "Friend")
	model = key(t, model, "enter")
	contacts, err := client.SearchContacts(context.Background(), "Renamed Friend")
	if err != nil || len(contacts) != 1 {
		t.Fatalf("contact not renamed: %v %v", contacts, err)
	}
	contact := contacts[0]
	model = key(t, model, "enter")
	model = key(t, model, "i")
	model = paste(t, model, "Keep this history")
	model = key(t, model, "ctrl+s")
	model = key(t, model, "c")
	model = paste(t, model, "Renamed Friend")
	model = key(t, model, "enter")
	model = key(t, model, "D")
	model = key(t, model, "y")
	contacts, _ = client.SearchContacts(context.Background(), "Renamed Friend")
	if len(contacts) != 0 {
		t.Fatal("contact not deleted")
	}
	messages := history(t, client, contact.ID)
	if len(messages) != 1 || messages[0].Text != "Keep this history" {
		t.Fatal("contact deletion removed messages")
	}
}
