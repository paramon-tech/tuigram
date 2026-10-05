package demo

import (
	"context"
	"errors"

	"github.com/paramon-tech/tuigram/internal/core"
)

var _ core.CallClient = (*Client)(nil)

func (c *Client) CallState() core.CallState { return core.CallState{Status: "idle"} }

func (c *Client) StartCall(ctx context.Context, _ core.Chat) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("native voice calls require a live Telegram session; demo mode never uses the microphone")
}

func (c *Client) AnswerCall(ctx context.Context, _ uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("there is no incoming call in demo mode")
}

func (c *Client) EndCall(ctx context.Context, _ uint64) error { return ctx.Err() }

func (c *Client) SetCallMuted(uint64, bool) error { return errors.New("there is no active call") }
