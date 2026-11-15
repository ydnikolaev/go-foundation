package editcoalescer

import (
	"context"
	"fmt"
	"time"
)

// deliver edits the object and reports the sequence to Hooks.Edited; a failure is only reported (the caller's answer is
// long sent). It reports whether the entry is dropped: a refused edit says the object or the data is refused, so a later
// put starts afresh.
func (c *Coalescer) deliver(k key, bot Editor, tag, data string) (drop bool) {
	start := c.cfg.Now()
	err := c.edit(k, bot, data)
	if c.cfg.Hooks.Edited != nil {
		c.cfg.Hooks.Edited(Result{Tag: tag, Object: k.object, OK: err == nil, Took: c.cfg.Now().Sub(start), Err: err})
	}
	return err != nil && c.cfg.Errors.Refused(err)
}

// edit calls the editor: "not modified" is a success; a rate limit holds the bot's edits and the call is retried once
// after the hold (none while flushing: Close sends once and moves on).
func (c *Coalescer) edit(k key, bot Editor, data string) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if werr := c.held(k.bot); werr != nil {
			return werr
		}
		ctx, cancel := context.WithTimeout(c.editCtx(), c.cfg.EditTimeout)
		err = bot.Edit(ctx, k.object, data)
		cancel()
		if err == nil || c.cfg.Errors.NotModified(err) {
			return nil
		}
		d, flood := c.cfg.Errors.FloodWait(err)
		if !flood || c.isFlushing() {
			return fmt.Errorf("edit: %w", err)
		}
		c.hold(k.bot, d)
	}
	return fmt.Errorf("edit: %w", err)
}

// hold keeps every edit of botID back for d: one window if d is not positive, at most MaxHold.
func (c *Coalescer) hold(botID string, d time.Duration) {
	if d <= 0 {
		d = c.cfg.Window
	}
	until := c.cfg.Now().Add(min(d, c.cfg.MaxHold))
	c.mu.Lock()
	defer c.mu.Unlock()
	if until.After(c.holds[botID]) {
		c.holds[botID] = until
	}
}

// held waits out the hold on botID, if any; Close ends the wait (the entry is then sent at once).
func (c *Coalescer) held(botID string) error {
	c.mu.Lock()
	until, ok := c.holds[botID]
	flushing := c.flushing
	c.mu.Unlock()
	left := until.Sub(c.cfg.Now())
	if !ok || flushing || left <= 0 {
		return nil
	}
	if err := c.cfg.Wait(c.wakeCx, left); err != nil && !c.isFlushing() {
		return err
	}
	return nil
}
