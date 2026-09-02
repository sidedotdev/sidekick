package sideagent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// ErrChannelClosed indicates the exec channel is no longer usable and a fresh
// one must be dialed.
var ErrChannelClosed = errors.New("agent exec channel closed")

// ErrNotSent indicates the channel was already known broken before the
// request was sent, so it provably never reached the server and retrying it
// on a fresh channel is safe even for non-idempotent commands.
var ErrNotSent = errors.New("agent exec request not sent")

// cancelResponseGrace bounds how long Exec waits, after requesting
// cancellation, for the server to confirm the command was reaped.
const cancelResponseGrace = 10 * time.Second

// Client drives the exec protocol over an established stdio channel (usually
// the stdin/stdout of an ssh process running `side-agent exec`). It is safe
// for concurrent use: requests are multiplexed onto the single channel and
// responses matched back by ID.
type Client struct {
	writeMu sync.Mutex
	w       io.Writer

	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan ExecResponse
	err     error
	closed  chan struct{}
}

// NewClient wraps a channel whose reads deliver server responses and whose
// writes carry requests to the server, and starts the response dispatch loop.
func NewClient(r io.Reader, w io.Writer) *Client {
	c := &Client{
		w:       w,
		pending: map[uint64]chan ExecResponse{},
		closed:  make(chan struct{}),
	}
	go c.readLoop(bufio.NewReader(r))
	return c
}

func (c *Client) readLoop(br *bufio.Reader) {
	for {
		var resp ExecResponse
		if err := readFrame(br, &resp); err != nil {
			c.fail(err)
			return
		}
		c.mu.Lock()
		ch := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

// fail marks the channel broken, waking all pending requests.
func (c *Client) fail(cause error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	c.err = fmt.Errorf("%w: %v", ErrChannelClosed, cause)
	close(c.closed)
}

// Close marks the client unusable and wakes all pending requests. It does not
// own the underlying transport; the caller tears that down separately.
func (c *Client) Close() {
	c.fail(errors.New("closed by client"))
}

func (c *Client) send(msg clientMessage) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeFrame(c.w, msg)
}

func (c *Client) brokenErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Exec sends one request and blocks until its response arrives, the channel
// breaks, or ctx is done. On ctx cancellation the server is asked to kill the
// command's process group, and Exec waits briefly for confirmation before
// returning ctx's error. Errors wrapping ErrNotSent mean the request provably
// never reached the server; any other error leaves execution state unknown.
func (c *Client) Exec(ctx context.Context, req ExecRequest) (ExecResponse, error) {
	ch := make(chan ExecResponse, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return ExecResponse{}, fmt.Errorf("%w: %w", ErrNotSent, err)
	}
	c.nextID++
	req.ID = c.nextID
	c.pending[req.ID] = ch
	c.mu.Unlock()

	if err := c.send(clientMessage{Exec: &req}); err != nil {
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
		// The write may have delivered a complete frame before failing, so
		// execution state is unknown; the channel is broken either way.
		c.fail(err)
		return ExecResponse{}, fmt.Errorf("send exec request: %w", c.brokenErr())
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-c.closed:
		return ExecResponse{}, c.brokenErr()
	case <-ctx.Done():
		_ = c.send(clientMessage{CancelID: req.ID})
		select {
		case <-ch:
		case <-c.closed:
		case <-time.After(cancelResponseGrace):
		}
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return ExecResponse{}, ctx.Err()
	}
}
