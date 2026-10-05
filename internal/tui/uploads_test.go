package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/paramon-tech/tuigram/internal/core"
)

type attachmentFake struct {
	*fakeClient
	sent       int
	chat       core.Chat
	attachment core.Attachment
	upload     func(context.Context) error
}

func (f *attachmentFake) SendAttachment(ctx context.Context, chat core.Chat, attachment core.Attachment) error {
	f.sent++
	f.chat, f.attachment = chat, attachment
	if f.upload != nil {
		return f.upload(ctx)
	}
	return f.fail
}

func attachmentFixture() (Model, *attachmentFake) {
	m, base := fixture()
	fake := &attachmentFake{fakeClient: base}
	m.client = fake
	return m, fake
}

func attachmentSpace(m Model) Model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	return next.(Model)
}

func TestAttachmentFormPreservesPhysicalSpacesAndCapturedTarget(t *testing.T) {
	m, fake := attachmentFixture()
	m, cmd := press(m, "a")
	if cmd != nil || m.mode != manageForm || m.form.kind != "attachment" || fake.sent != 0 {
		t.Fatal("attachment shortcut did not open an unsent form")
	}
	m, _ = press(m, `"/tmp/my`)
	m = attachmentSpace(m)
	m, _ = press(m, `photo.png"`)
	m, _ = press(m, "enter")
	m, _ = press(m, "With")
	m = attachmentSpace(m)
	m, _ = press(m, "spaces 🙂")
	// A background selection change must not redirect an attachment prepared
	// for a different chat, or refresh the wrong history on completion.
	m.chatIndex = 1
	m, cmd = press(m, "ctrl+s")
	if cmd == nil || !m.busy || fake.sent != 0 {
		t.Fatal("send did not defer network work or mark itself busy")
	}
	defer m.uploadCancel()
	next, refresh := m.Update(cmd())
	m = next.(Model)
	if fake.sent != 1 || fake.chat.ID != "one" || fake.attachment.Path != "/tmp/my photo.png" || fake.attachment.Caption != "With spaces 🙂" {
		t.Fatalf("attachment target or spaces lost: %d %+v %+v", fake.sent, fake.chat, fake.attachment)
	}
	if refresh != nil || m.busy || m.mode != normal || m.form.kind != "" || m.uploadCancel != nil {
		t.Fatal("upload completion refreshed the wrong chat or retained its form")
	}
}

func TestAttachmentEmptyCaptionAndSuccessRefreshHistory(t *testing.T) {
	m, fake := attachmentFixture()
	m, _ = press(m, "a")
	m, _ = press(m, "/tmp/photo.png")
	m, _ = press(m, "enter")
	m, cmd := press(m, "enter")
	if cmd == nil || !m.busy {
		t.Fatalf("empty caption was rejected: %s", m.failure)
	}
	defer m.uploadCancel()
	fake.histories["one"] = append(fake.histories["one"], core.Message{ID: 99, ChatID: "one", Image: true, Downloadable: true})
	next, refresh := m.Update(cmd())
	m = next.(Model)
	if refresh == nil || m.mode != normal || fake.attachment.Caption != "" || m.busy {
		t.Fatal("successful captionless upload did not refresh history")
	}
	m = complete(m, refresh)
	if len(m.messages) != 3 || m.messages[2].ID != 99 || m.loading || fake.calls[len(fake.calls)-1] != "history:one:" {
		t.Fatalf("history did not show sent attachment: %+v, calls=%v", m.messages, fake.calls)
	}
}

func TestAttachmentFailureKeepsEditableFormAndPreventsDoubleSend(t *testing.T) {
	m, fake := attachmentFixture()
	m, _ = press(m, "a")
	m, _ = press(m, "/tmp/my video.mp4")
	m, _ = press(m, "tab")
	m, _ = press(m, "A caption")
	fake.fail = errors.New("permission denied")
	m, cmd := press(m, "ctrl+s")
	if cmd == nil {
		t.Fatal("send command missing")
	}
	defer m.uploadCancel()
	m, duplicate := press(m, "ctrl+s")
	if duplicate != nil {
		t.Fatal("Ctrl+s started a duplicate upload while busy")
	}
	m, _ = press(m, "altered")
	m = complete(m, cmd)
	if m.busy || m.mode != manageForm || m.form.values[0] != "/tmp/my video.mp4" || m.form.values[1] != "A caption" || !strings.Contains(m.failure, "permission denied") || fake.sent != 1 {
		t.Fatalf("failed upload lost retryable form: %+v failure=%s sends=%d", m.form, m.failure, fake.sent)
	}
	m, _ = press(m, " corrected")
	if m.form.values[1] != "A caption corrected" {
		t.Fatal("failed upload form cannot be edited")
	}
	fake.fail = nil
	m, retry := press(m, "ctrl+s")
	if retry == nil {
		t.Fatal("corrected form did not allow an explicit retry")
	}
	m = complete(m, retry)
	if fake.sent != 2 || fake.attachment.Caption != "A caption corrected" || m.mode != normal {
		t.Fatal("explicit retry lost corrected attachment")
	}
}

func TestAttachmentEscapeCancelsRunningUploadAndKeepsForm(t *testing.T) {
	m, fake := attachmentFixture()
	started := make(chan struct{})
	fake.upload = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	m, _ = press(m, "a")
	m, _ = press(m, "/tmp/photo.png")
	m, cmd := press(m, "ctrl+s")
	if cmd == nil {
		t.Fatal("send command missing")
	}
	defer m.uploadCancel()
	finished := make(chan tea.Msg, 1)
	go func() { finished <- cmd() }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("upload did not start")
	}
	m, _ = press(m, "esc")
	if !m.busy || m.mode != manageForm {
		t.Fatal("cancel should keep the form busy until the upload stops")
	}
	if _, duplicate := press(m, "ctrl+s"); duplicate != nil {
		t.Fatal("canceling upload could be submitted twice")
	}
	select {
	case result := <-finished:
		next, refresh := m.Update(result)
		m = next.(Model)
		if refresh != nil {
			t.Fatal("canceled upload requested success refresh")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Escape did not cancel upload context")
	}
	if m.busy || m.mode != manageForm || m.form.values[0] != "/tmp/photo.png" || m.uploadCancel != nil || !strings.Contains(m.failure, "Upload stopped") {
		t.Fatalf("cancellation lost form or status: %+v %s", m.form, m.failure)
	}
}

func TestAttachmentRequiresPathAndDoesNotBreakComposeDraft(t *testing.T) {
	m, fake := attachmentFixture()
	m.drafts["one"] = "an unsent text message"
	m, _ = press(m, "a")
	m, cmd := press(m, "ctrl+s")
	if cmd != nil || m.busy || fake.sent != 0 || m.form.index != 0 || !strings.Contains(m.failure, "file path") {
		t.Fatal("empty path did not remain in form for correction")
	}
	m, _ = press(m, "esc")
	m, _ = press(m, "i")
	if m.mode != compose || m.input != "an unsent text message" {
		t.Fatal("attachment form discarded the text draft")
	}
}

type batchAttachmentFake struct {
	*attachmentFake
	attachments []core.Attachment
}

func (f *batchAttachmentFake) SendAttachments(_ context.Context, chat core.Chat, attachments []core.Attachment) error {
	f.sent++
	f.chat = chat
	f.attachments = append([]core.Attachment(nil), attachments...)
	return f.fail
}

func TestAttachmentAlbumEditorPreservesPathsCaptionAndFileMode(t *testing.T) {
	m, single := attachmentFixture()
	fake := &batchAttachmentFake{attachmentFake: single}
	m.client = fake
	m, _ = press(m, "a")
	m, _ = press(m, "/tmp/photo one.png")
	m, _ = press(m, "tab")
	m, _ = press(m, "shared caption")
	m, _ = press(m, "ctrl+n")
	if m.form.index != 1 || m.form.values[2] != "shared caption" {
		t.Fatal("adding path did not preserve final caption")
	}
	m, _ = press(m, "'/tmp/photo two.png'")
	m, _ = press(m, "ctrl+f")
	m, cmd := press(m, "ctrl+s")
	if cmd == nil || !m.busy {
		t.Fatal("album upload did not start")
	}
	m = complete(m, cmd)
	if fake.sent != 1 || len(fake.attachments) != 2 || fake.chat.ID != "one" {
		t.Fatalf("album sent incorrectly: %+v", fake)
	}
	for i, attachment := range fake.attachments {
		want := "/tmp/photo one.png"
		if i == 1 {
			want = "/tmp/photo two.png"
		}
		if attachment.Path != want || attachment.Kind != "document" || (i == 0 && attachment.Caption != "shared caption") || (i == 1 && attachment.Caption != "") {
			t.Fatalf("album lost path/file mode/caption: %+v", fake.attachments)
		}
	}
}

func TestAttachmentAlbumRemoveLimitAndEmptyFile(t *testing.T) {
	m, single := attachmentFixture()
	fake := &batchAttachmentFake{attachmentFake: single}
	m.client = fake
	m, _ = press(m, "a")
	m, _ = press(m, "/tmp/one.png")
	m, _ = press(m, "ctrl+n")
	m, cmd := press(m, "ctrl+s")
	if cmd != nil || fake.sent != 0 || m.form.index != 1 {
		t.Fatal("empty second file was submitted")
	}
	m, _ = press(m, "ctrl+d")
	if len(m.form.values) != 2 || m.form.values[0] != "/tmp/one.png" {
		t.Fatal("removing file lost first path or caption")
	}
	for range core.MaxAttachmentCount + 1 {
		m, _ = press(m, "ctrl+n")
	}
	if len(m.form.values) != core.MaxAttachmentCount+1 || !strings.Contains(m.failure, "at most 10") {
		t.Fatalf("attachment limit not enforced: %d %s", len(m.form.values), m.failure)
	}
}
