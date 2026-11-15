// Package editcoalescer sends the latest data of each remote object at most once per window. One goroutine per live
// object waits the window, sends whatever was put last (the trailing edge) and goes on while puts keep landing during
// the edit; the object leaves the map when it is quiet. A sender's rate-limit answer holds that sender's edits back
// (hold.go), and Close flushes what is waiting.
//
// It is a unit: stdlib only, nothing of the host project. The edit call and the reading of its errors are injected
// (Editor, Reader); what happened is reported through Hooks, so the caller owns every log line.
package editcoalescer

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// The defaults of a Config field left at zero.
const (
	// DefaultWindow is the spacing of edits to one object.
	DefaultWindow = 1200 * time.Millisecond
	// DefaultEditTimeout bounds one edit call.
	DefaultEditTimeout = 5 * time.Second
	// DefaultMaxHold caps the rate-limit wait a sender's edits sit out before the one retry.
	DefaultMaxHold = time.Minute
	// DefaultMaxEntries bounds the live objects; a Put past it is dropped.
	DefaultMaxEntries = 4096
	// DefaultCloseBound bounds Close in all.
	DefaultCloseBound = 5 * time.Second
	// DefaultOverflowEvery spaces the Hooks.Full calls of a full coalescer.
	DefaultOverflowEvery = time.Minute
)

// Editor sends the data of one object. Edits of one coalescer may run at once, for different objects.
type Editor interface {
	Edit(ctx context.Context, object, data string) error
}

// Reader reads what an Editor's error means.
type Reader interface {
	// NotModified reports that the remote already holds the data: the edit counts as a success.
	NotModified(err error) bool
	// Refused reports an answer that sending again would get again: the object's entry is dropped, not retried.
	Refused(err error) bool
	// FloodWait reports a rate limit and how long the remote asks to wait; a d of zero or less is one window.
	FloodWait(err error) (d time.Duration, ok bool)
}

// Result is one finished edit sequence of an object (the retry after a hold included).
type Result struct {
	Tag, Object string
	OK          bool
	Took        time.Duration
	Err         error // nil when OK
}

// Hooks report what the coalescer did; a nil one is skipped. A hook must not call back into the coalescer.
type Hooks struct {
	// Edited is called once per edit sequence, after it.
	Edited func(Result)
	// Full is called when a Put is dropped for a full map, at most once per OverflowEvery; dropped counts the Puts
	// dropped since the last call.
	Full func(tag string, limit, dropped int)
	// Dropped is called by Close, once, with the number of objects it gave up on at its bound.
	Dropped func(n int)
}

// Config builds a Coalescer. A zero field takes its default; a nil Now is time.Now, a nil Wait a real timer, a nil Errors
// a Reader that finds no special error.
type Config struct {
	Window, EditTimeout, MaxHold, CloseBound, OverflowEvery time.Duration
	MaxEntries                                              int
	Now                                                     func() time.Time
	Wait                                                    func(ctx context.Context, d time.Duration) error
	Errors                                                  Reader
	Hooks                                                   Hooks
}

func (cfg Config) withDefaults() Config {
	if cfg.Window <= 0 {
		cfg.Window = DefaultWindow
	}
	if cfg.EditTimeout <= 0 {
		cfg.EditTimeout = DefaultEditTimeout
	}
	if cfg.MaxHold <= 0 {
		cfg.MaxHold = DefaultMaxHold
	}
	if cfg.CloseBound <= 0 {
		cfg.CloseBound = DefaultCloseBound
	}
	if cfg.OverflowEvery <= 0 {
		cfg.OverflowEvery = DefaultOverflowEvery
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = DefaultMaxEntries
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Wait == nil {
		cfg.Wait = sleepCtx
	}
	if cfg.Errors == nil {
		cfg.Errors = plainErrors{}
	}
	return cfg
}

// plainErrors is the Reader of a caller that gave none: every error is a plain failure, kept for the next Put.
type plainErrors struct{}

func (plainErrors) NotModified(error) bool                { return false }
func (plainErrors) Refused(error) bool                    { return false }
func (plainErrors) FloodWait(error) (time.Duration, bool) { return 0, false }

type key struct{ bot, object string }

// entry is the write waiting for its window: the latest data, the editor and tag of the latest Put.
type entry struct {
	bot   Editor
	tag   string
	data  string
	dirty bool // a Put landed while the edit was in flight
}

// Coalescer sends an object's data at most once per window, the latest Put winning. Close flushes what is waiting.
type Coalescer struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc
	wakeCx context.Context // the waits' context: Close cancels it to end every window at once
	wake   context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	flushing bool            // Close is sending what waits: no more waits, one attempt per entry
	sendCtx  context.Context // the edits' context while flushing: bounded by Close's ctx and CloseBound
	m        map[key]*entry
	holds    map[string]time.Time // bot id -> when its rate limit ends
	lastFull time.Time            // the last Hooks.Full call
	fullN    int                  // puts dropped since that call
}

// New builds a coalescer under parent.
func New(parent context.Context, cfg Config) *Coalescer {
	ctx, cancel := context.WithCancel(parent)
	wakeCx, wake := context.WithCancel(ctx)
	return &Coalescer{cfg: cfg.withDefaults(), ctx: ctx, cancel: cancel, wakeCx: wakeCx, wake: wake,
		m: map[key]*entry{}, holds: map[string]time.Time{}}
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait: %w", ctx.Err())
	}
}

// Put queues data for object on bot (a sender id, the key of holds); it never blocks. tag is the caller's label, handed
// back in Hooks. A Put that finds the object waiting only replaces its data, editor and tag. After Close it does
// nothing; past MaxEntries live objects it drops the data.
func (c *Coalescer) Put(bot Editor, botID, tag, object, data string) {
	k := key{bot: botID, object: object}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if e, ok := c.m[k]; ok {
		e.bot, e.tag, e.data, e.dirty = bot, tag, data, true
		return
	}
	if len(c.m) >= c.cfg.MaxEntries {
		c.dropFull(tag)
		return
	}
	e := &entry{bot: bot, tag: tag, data: data}
	c.m[k] = e
	c.wg.Add(1)
	go c.run(k, e)
}

// Len is the number of live objects: waiting for their window or being edited.
func (c *Coalescer) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// Wait returns when no edit goroutine runs: every object put so far is sent or dropped.
func (c *Coalescer) Wait() { c.wg.Wait() }

// dropFull counts a put dropped for a full map and calls Hooks.Full once per OverflowEvery. c.mu is held.
func (c *Coalescer) dropFull(tag string) {
	c.fullN++
	if now := c.cfg.Now(); now.Sub(c.lastFull) >= c.cfg.OverflowEvery {
		if c.cfg.Hooks.Full != nil {
			c.cfg.Hooks.Full(tag, c.cfg.MaxEntries, c.fullN)
		}
		c.lastFull, c.fullN = now, 0
	}
}

func (c *Coalescer) run(k key, e *entry) {
	defer c.wg.Done()
	for c.pause() {
		c.mu.Lock()
		bot, tag, data := e.bot, e.tag, e.data
		e.dirty = false
		c.mu.Unlock()

		drop := c.deliver(k, bot, tag, data)

		c.mu.Lock()
		if drop || !e.dirty {
			delete(c.m, k)
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
	}
	c.mu.Lock()
	delete(c.m, k)
	c.mu.Unlock()
}

// pause waits the window; it reports whether to send now: the window passed, or Close wants the entry flushed.
func (c *Coalescer) pause() bool {
	if c.isFlushing() {
		return true
	}
	if err := c.cfg.Wait(c.wakeCx, c.cfg.Window); err != nil {
		return c.isFlushing()
	}
	return true
}

func (c *Coalescer) isFlushing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.flushing
}

// editCtx is the context of one edit call: the coalescer's, or Close's while flushing.
func (c *Coalescer) editCtx() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flushing {
		return c.sendCtx
	}
	return c.ctx
}

// Close sends each waiting entry once at once (one edit per object since its last), bounded by CloseBound in all and by
// ctx; what is left is dropped and reported to Hooks.Dropped. After Close, Put does nothing.
func (c *Coalescer) Close(ctx context.Context) {
	sendCtx, stop := context.WithCancel(ctx)
	defer stop()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed, c.flushing, c.sendCtx = true, true, sendCtx
	c.mu.Unlock()

	go func() {
		if c.cfg.Wait(sendCtx, c.cfg.CloseBound) == nil {
			stop()
		}
	}()
	c.wake()
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-sendCtx.Done():
		if left := c.Len(); left > 0 && c.cfg.Hooks.Dropped != nil {
			c.cfg.Hooks.Dropped(left)
		}
	}
	c.cancel()
}
