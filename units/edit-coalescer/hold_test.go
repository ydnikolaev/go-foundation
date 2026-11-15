package editcoalescer

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestHold(t *testing.T) {
	bg := context.Background()
	newC := func(t *testing.T, cfg Config) *Coalescer {
		t.Helper()
		cfg.Errors = codeReader{}
		c := New(bg, cfg)
		t.Cleanup(func() { c.Close(bg) })
		return c
	}

	t.Run("a 429 holds every object of the bot and not another bot's", func(t *testing.T) {
		rec, fw := &recorder{errs: []error{flood(5 * time.Second)}}, &fakeWait{} // a static clock: the hold stands
		c := newC(t, Config{Now: newClock().Now, Wait: fw.wait})
		step := func(botID, object string, want ...time.Duration) {
			t.Helper()
			c.Put(rec, botID, "tag", object, "x")
			c.Wait()
			if got := fw.take(); !slices.Equal(got, want) {
				t.Errorf("%s/%s waits = %v, want %v", botID, object, got, want)
			}
		}
		step("bot1", "W1", DefaultWindow, 5*time.Second) // the 429 and its one retry after the hold
		step("bot1", "W2", DefaultWindow, 5*time.Second) // another object of the bot waits the hold out
		step("bot2", "W1", DefaultWindow)                // another bot is free
		if n := len(rec.got()); n != 4 {
			t.Errorf("edits = %d, want 4 (W1 twice, W2, bot2's W1)", n)
		}
	})

	t.Run("a hold that has run out is not waited", func(t *testing.T) {
		clk, fw := newClock(), &fakeWait{}
		rec := &recorder{errs: []error{flood(5 * time.Second), flood(5 * time.Second)}}
		c := newC(t, Config{Now: clk.Now, Wait: fw.wait})
		c.Put(rec, "bot1", "tag", "W", "old")
		c.Wait()
		fw.take()
		clk.Add(5 * time.Second)
		c.Put(rec, "bot1", "tag", "W", "new")
		c.Wait()
		if got := fw.take(); !slices.Equal(got, []time.Duration{DefaultWindow}) {
			t.Errorf("waits = %v, want one window", got)
		}
	})

	retries := []struct {
		name      string
		maxHold   time.Duration
		errs      []error
		wantWaits []time.Duration
		wantEdits int
		wantOK    bool
	}{
		{"no number waits one window", 0, []error{flood(0)}, []time.Duration{DefaultWindow, DefaultWindow}, 2, true},
		{"a second 429 is not retried again", 0, []error{flood(2 * time.Second), flood(2 * time.Second)}, []time.Duration{DefaultWindow, 2 * time.Second}, 2, false},
		{"the hold is capped at MaxHold", 0, []error{flood(99999 * time.Second), flood(99999 * time.Second)}, []time.Duration{DefaultWindow, DefaultMaxHold}, 2, false},
		{"a configured MaxHold caps it", 3 * time.Second, []error{flood(time.Minute), flood(time.Minute)}, []time.Duration{DefaultWindow, 3 * time.Second}, 2, false},
		{"a refused edit is not retried", 0, []error{&remoteErr{code: 400, text: "nope"}}, []time.Duration{DefaultWindow}, 1, false},
		{"a transport error is not retried", 0, []error{errors.New("connection reset")}, []time.Duration{DefaultWindow}, 1, false},
	}
	for _, tt := range retries {
		t.Run(tt.name, func(t *testing.T) {
			rec, fw, rs := &recorder{errs: tt.errs}, &fakeWait{}, &results{}
			c := newC(t, Config{MaxHold: tt.maxHold, Now: newClock().Now, Wait: fw.wait, Hooks: Hooks{Edited: rs.add}})
			c.Put(rec, "bot1", "tag", "W", "x")
			c.Wait()
			if n := len(rec.got()); n != tt.wantEdits {
				t.Errorf("edits = %d, want %d", n, tt.wantEdits)
			}
			if got := fw.take(); !slices.Equal(got, tt.wantWaits) {
				t.Errorf("waits = %v, want %v", got, tt.wantWaits)
			}
			if got := rs.all(); len(got) != 1 || got[0].OK != tt.wantOK {
				t.Errorf("results = %+v, want one with OK=%v", got, tt.wantOK)
			}
			if n := c.Len(); n != 0 {
				t.Errorf("%d entries live after the drop, want none", n)
			}
		})
	}

	t.Run("after a dropped edit the next put starts afresh", func(t *testing.T) {
		clk, fw := newClock(), &fakeWait{}
		rec := &recorder{errs: []error{flood(99999 * time.Second), flood(99999 * time.Second)}}
		c := newC(t, Config{Now: clk.Now, Wait: fw.wait})
		c.Put(rec, "bot1", "tag", "W", "old")
		c.Wait()
		fw.take()
		clk.Add(DefaultMaxHold) // the hold has run out
		c.Put(rec, "bot1", "tag", "W", "new")
		c.Wait()
		calls := rec.got()
		if len(calls) != 3 || calls[2] != (call{"W", "new"}) {
			t.Fatalf("edits = %+v, want the dropped one twice, then the new data", calls)
		}
		if got := fw.take(); !slices.Equal(got, []time.Duration{DefaultWindow}) {
			t.Errorf("waits = %v, want one window", got)
		}
	})
}
