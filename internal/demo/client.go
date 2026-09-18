// Package demo provides an isolated in-memory Telegram-like workspace.
package demo

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"time"

	"github.com/paramon-tech/tuigram/internal/core"
)

// Client never connects to Telegram or persists user data.
type Client struct {
	mu       sync.Mutex
	chats    []core.Chat
	messages map[string][]core.Message
	contacts []core.Contact
	nextID   int
}

var _ core.Client = (*Client)(nil)

func New() *Client {
	t := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	return &Client{
		chats: []core.Chat{{ID: "saved", Title: "Saved Messages", Kind: "private"}, {ID: "builders", Title: "Tuigram builders", Kind: "group", Unread: 2}, {ID: "news", Title: "Telegram updates", Kind: "channel"}},
		messages: map[string][]core.Message{
			"saved":    {{ID: 1, ChatID: "saved", Sender: "You", Text: "Welcome to tuigram 👋\nYour conversations, at keyboard speed.", Time: t, Outgoing: true}, {ID: 2, ChatID: "saved", Sender: "You", Text: "Press ? for help. Try i to compose, / to search, and Tab to switch panes.\nhttps://github.com/paramon-tech/tuigram", Time: t.Add(time.Minute), Outgoing: true, Reactions: []core.Reaction{{Emoji: "👍", Count: 3}}}, {ID: 3, ChatID: "saved", Sender: "You", Text: "A little terminal sunset. Select this message and press v to preview.", Time: t.Add(2 * time.Minute), Image: true, MediaLabel: "photo"}},
			"builders": {{ID: 4, ChatID: "builders", Sender: "Ada", Text: "Ship small. Test thoroughly. 🚀", Time: t, Reactions: []core.Reaction{{Emoji: "❤", Count: 2}}}, {ID: 5, ChatID: "builders", Sender: "Linus", Text: "Vim keys, a quiet terminal, and good company.", Time: t.Add(time.Minute)}},
			"news":     {{ID: 6, ChatID: "news", Sender: "Telegram updates", Text: "Demo channel: posts, forwarding, and reactions all work locally.", Time: t}},
		},
		contacts: []core.Contact{{ID: "ada", Name: "Ada Lovelace", Username: "ada"}, {ID: "linus", Name: "Linus", Username: "linus"}}, nextID: 7,
	}
}

func (c *Client) Dialogs(ctx context.Context) ([]core.Chat, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]core.Chat(nil), c.chats...), nil
}

func (c *Client) History(ctx context.Context, chat core.Chat, query string) ([]core.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasPeer(chat.ID) {
		return nil, errors.New("chat not found")
	}
	var result []core.Message
	for _, m := range c.messages[chat.ID] {
		if query == "" || strings.Contains(strings.ToLower(m.Text), strings.ToLower(query)) {
			m.Reactions = append([]core.Reaction(nil), m.Reactions...)
			result = append(result, m)
		}
	}
	return result, nil
}

func (c *Client) Send(ctx context.Context, chat core.Chat, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("message cannot be empty")
	}
	if len([]rune(body)) > 4096 {
		return errors.New("message exceeds 4096 characters")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasPeer(chat.ID) {
		return errors.New("chat not found")
	}
	if !c.hasChat(chat.ID) {
		for _, ct := range c.contacts {
			if ct.ID == chat.ID {
				c.chats = append(c.chats, core.Chat{ID: ct.ID, Title: ct.Name, Kind: "private"})
				break
			}
		}
	}
	c.messages[chat.ID] = append(c.messages[chat.ID], core.Message{ID: c.nextID, ChatID: chat.ID, Sender: "You", Text: body, Time: time.Now(), Outgoing: true})
	c.nextID++
	return nil
}

func (c *Client) Forward(ctx context.Context, from core.Chat, id int, to core.Chat) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasChat(to.ID) {
		return errors.New("destination not found")
	}
	for _, m := range c.messages[from.ID] {
		if m.ID == id {
			m.ID = c.nextID
			c.nextID++
			m.ChatID = to.ID
			m.Forwarded = true
			m.Outgoing = true
			m.Time = time.Now()
			m.Reactions = nil
			c.messages[to.ID] = append(c.messages[to.ID], m)
			return nil
		}
	}
	return errors.New("message not found")
}

func (c *Client) React(ctx context.Context, chat core.Chat, id int, emoji string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, m := range c.messages[chat.ID] {
		if m.ID == id {
			for j, r := range m.Reactions {
				if r.Emoji == emoji {
					c.messages[chat.ID][i].Reactions[j].Count++
					return nil
				}
			}
			c.messages[chat.ID][i].Reactions = append(m.Reactions, core.Reaction{Emoji: emoji, Count: 1})
			return nil
		}
	}
	return errors.New("message not found")
}

func (c *Client) SearchContacts(ctx context.Context, query string) ([]core.Contact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []core.Contact
	query = strings.ToLower(strings.TrimPrefix(query, "@"))
	for _, ct := range c.contacts {
		if strings.Contains(strings.ToLower(ct.Name+" "+ct.Username), query) {
			result = append(result, ct)
		}
	}
	return result, nil
}

func (c *Client) AddContact(ctx context.Context, ct core.Contact) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, known := range c.contacts {
		if known.ID == ct.ID {
			if !c.hasChat(ct.ID) {
				c.chats = append(c.chats, core.Chat{ID: ct.ID, Title: ct.Name, Kind: "private"})
			}
			return nil
		}
	}
	return errors.New("contact not found")
}

func (c *Client) DownloadImage(ctx context.Context, chat core.Chat, id int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	found := false
	for _, m := range c.messages[chat.ID] {
		if m.ID == id && m.Image {
			found = true
		}
	}
	if !found {
		return nil, errors.New("message has no photo")
	}
	img := image.NewRGBA(image.Rect(0, 0, 120, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 120; x++ {
			v := color.RGBA{uint8(40 + y*3), uint8(35 + y), uint8(100 + y), 255}
			if (x-85)*(x-85)+(y-24)*(y-24) < 144 {
				v = color.RGBA{255, 210, 120, 255}
			}
			if y > 42+x/15 {
				v = color.RGBA{20, 45, 60, 255}
			}
			img.Set(x, y, v)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c *Client) hasChat(id string) bool {
	for _, chat := range c.chats {
		if chat.ID == id {
			return true
		}
	}
	return false
}

func (c *Client) hasPeer(id string) bool {
	if c.hasChat(id) {
		return true
	}
	for _, ct := range c.contacts {
		if ct.ID == id {
			return true
		}
	}
	return false
}
