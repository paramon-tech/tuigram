package telegram

import (
	"context"
	"time"

	"github.com/gotd/td/tg"
)

// Background dialog refreshes share folder rules for a minute. Opening the
// organization panel or editing a folder always fetches current server rules.
func (c *client) loadDialogFilters(ctx context.Context, cached bool) (*tg.MessagesDialogFilters, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.RLock()
	response, at, generation := c.dialogFilters, c.dialogFiltersAt, c.filterGeneration
	c.mu.RUnlock()
	if cached && response != nil && time.Since(at) < time.Minute {
		return response, nil
	}
	response, err := c.api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	// A fetch started before a local edit must not refill the invalidated cache.
	if c.filterGeneration == generation {
		c.dialogFilters, c.dialogFiltersAt = response, time.Now()
	}
	c.mu.Unlock()
	return response, nil
}

func (c *client) invalidateDialogFilters() {
	c.mu.Lock()
	c.filterGeneration++
	c.dialogFilters = nil
	c.mu.Unlock()
}
