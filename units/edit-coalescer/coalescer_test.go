package editcoalescer

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// remoteErr is a remote's answer: a code, a text and, for 429, how long it asks to wait.
type remoteErr struct {
	code  int
	text  string
	after time.Duration
}

func (e *remoteErr) Error() string { return fmt.Sprintf("remote %d: %s", e.code, e.text) }

// codeReader reads remoteErr the way a chat API's adapter would.
type codeReader struct{}

func (codeReader) NotModified(err error) bool {
	var re *remoteErr
	return errors.As(err, &re) && strings.Contains(re.text, "not modified")
}

func (codeReader) Refused(err error) bool {
	var re *remoteErr
	return errors.As(err, &re) && re.code >= 400 && re.code < 500
}

func (codeReader) FloodWait(err error) (time.Duration, bool) {
	var re *remoteErr
	if !errors.As(err, &re) || re.code != 429 {
		return 0, false
	}
	return re.after, true
}

func flood(after time.Duration) error { return &remoteErr{code: 429, text: "slow down", after: after} }

type call struct{ object, data string }

// recorder is an Editor that keeps its calls and answers with the next of errs (nil once they are used up).
type recorder struct {
	mu     sync.Mutex
	calls  []call
	errs   []error
	onCall func(call) // runs inside the edit, before it answers
}

func (r *recorder) Edit(_ context.Context, object, data string) error {
	cl := call{object, data}
	r.mu.Lock()
	r.calls = append(r.calls, cl)
	var err error
	if len(r.errs) > 0 {
		err, r.errs = r.errs[0], r.errs[1:]
	}
	hook := r.onCall
	r.mu.Unlock()
	if hook != nil {
		hook(cl)
	}
	return err
}

func (r *recorder) got() []call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// editFunc is an Editor made of a function.
type editFunc func(ctx context.Context, object, data string) error

func (f editFunc) Edit(ctx context.Context, object, data string) error { return f(ctx, object, data) }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeWait is a wait that records each duration and returns at once, moving the clock by it when clk is set.
type fakeWait struct {
	clk *clock
	mu  sync.Mutex
	d   []time.Duration
}

func (f *fakeWait) wait(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fake wait: %w", err)
	}
	f.mu.Lock()
	f.d = append(f.d, d)
	f.mu.Unlock()
	if f.clk != nil {
		f.clk.Add(d)
	}
	return nil
}

// take returns the waits recorded since the last take.
func (f *fakeWait) take() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.d
	f.d = nil
	return out
}

// gate is a wait that reports each wait it entered and blocks until release is closed or its context ends, so a test
// decides when a window "passes".
type gate struct {
	entered chan time.Duration
	release chan struct{}
}

func newGate() *gate {
	return &gate{entered: make(chan time.Duration, 1024), release: make(chan struct{})}
}

func (g *gate) wait(ctx context.Context, d time.Duration) error {
	g.entered <- d
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("gate: %w", ctx.Err())
	}
}

// await returns the next wait a goroutine entered.
func (g *gate) await(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-g.entered:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("no goroutine entered its wait")
		return 0
	}
}

// eventually waits for cond, yielding the processor between looks.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		runtime.Gosched()
	}
}

// results collects Hooks.Edited.
type results struct {
	mu sync.Mutex
	r  []Result
}

func (rs *results) add(r Result) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.r = append(rs.r, r)
}

func (rs *results) all() []Result {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return slices.Clone(rs.r)
}

func TestCoalesce(t *testing.T) {
	bg := context.Background()
	newC := func(t *testing.T, cfg Config) *Coalescer {
		t.Helper()
		c := New(bg, cfg)
		t.Cleanup(func() { c.Close(bg) })
		return c
	}

	t.Run("ten puts in one window give one edit with the last data", func(t *testing.T) {
		rec, g := &recorder{}, newGate()
		c := newC(t, Config{Now: newClock().Now, Wait: g.wait})
		c.Put(rec, "bot1", "tag", "W", "first")
		if d := g.await(t); d != DefaultWindow {
			t.Fatalf("first put waits %v, want %v", d, DefaultWindow)
		}
		for i := 1; i < 10; i++ {
			c.Put(rec, "bot1", "tag", "W", fmt.Sprint("put", i))
		}
		close(g.release)
		c.Wait()
		if got, want := rec.got(), []call{{"W", "put9"}}; !slices.Equal(got, want) {
			t.Errorf("edits = %+v, want %+v", got, want)
		}
		if len(g.entered) != 0 {
			t.Errorf("%d extra waits, want one window", len(g.entered))
		}
		if n := c.Len(); n != 0 {
			t.Errorf("%d entries live after the send, want none", n)
		}
	})

	t.Run("a configured window is the one waited", func(t *testing.T) {
		rec, g := &recorder{}, newGate()
		c := newC(t, Config{Window: 250 * time.Millisecond, Wait: g.wait})
		c.Put(rec, "bot1", "tag", "W", "x")
		if d := g.await(t); d != 250*time.Millisecond {
			t.Errorf("waits %v, want 250ms", d)
		}
		close(g.release)
		c.Wait()
	})

	t.Run("objects and bots are independent", func(t *testing.T) {
		rec, g := &recorder{}, newGate()
		c := newC(t, Config{Now: newClock().Now, Wait: g.wait})
		c.Put(rec, "bot1", "tag", "W1", "a")
		c.Put(rec, "bot1", "tag", "W2", "b")
		c.Put(rec, "bot2", "tag", "W1", "c")
		for range 3 {
			g.await(t)
		}
		close(g.release)
		c.Wait()
		got := rec.got()
		slices.SortFunc(got, func(a, b call) int { return strings.Compare(a.object+a.data, b.object+b.data) })
		if want := []call{{"W1", "a"}, {"W1", "c"}, {"W2", "b"}}; !slices.Equal(got, want) {
			t.Errorf("edits = %+v, want one per object and bot", got)
		}
	})

	t.Run("the editor of the latest put sends", func(t *testing.T) {
		old, latest, g := &recorder{}, &recorder{}, newGate()
		c := newC(t, Config{Wait: g.wait})
		c.Put(old, "bot1", "tag", "W", "a")
		g.await(t)
		c.Put(latest, "bot1", "tag", "W", "b")
		close(g.release)
		c.Wait()
		if len(old.got()) != 0 || !slices.Equal(latest.got(), []call{{"W", "b"}}) {
			t.Errorf("old = %v, latest = %v, want the edit on the latest editor only", old.got(), latest.got())
		}
	})

	t.Run("concurrent puts give one edit with the last data", func(t *testing.T) {
		rec, g := &recorder{}, newGate()
		c := newC(t, Config{Now: newClock().Now, Wait: g.wait})
		var wg sync.WaitGroup
		for i := range 64 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.Put(rec, "bot1", "tag", "W", fmt.Sprint("p", i))
			}()
		}
		wg.Wait()
		c.Put(rec, "bot1", "tag", "W", "last")
		g.await(t)
		close(g.release)
		c.Wait()
		if got, want := rec.got(), []call{{"W", "last"}}; !slices.Equal(got, want) {
			t.Errorf("edits = %+v, want %+v", got, want)
		}
	})

	t.Run("a put during the edit goes out after another window", func(t *testing.T) {
		fw := &fakeWait{}
		c := newC(t, Config{Wait: fw.wait})
		rec := &recorder{}
		again := true
		ed := editFunc(func(ctx context.Context, object, data string) error {
			if again {
				again = false
				c.Put(rec, "bot1", "tag", "W", "later")
			}
			return rec.Edit(ctx, object, data)
		})
		c.Put(ed, "bot1", "tag", "W", "now")
		c.Wait()
		if got, want := rec.got(), []call{{"W", "now"}, {"W", "later"}}; !slices.Equal(got, want) {
			t.Errorf("edits = %+v, want %+v", got, want)
		}
		if got := fw.take(); !slices.Equal(got, []time.Duration{DefaultWindow, DefaultWindow}) {
			t.Errorf("waits = %v, want a window before each edit", got)
		}
	})

	drops := []struct {
		name      string
		err       error
		wantEdits int // 2: the put that landed during the edit is sent; 1: the entry is dropped
	}{
		{"a refused edit drops the entry", &remoteErr{code: 400, text: "bad"}, 1},
		{"a 5xx keeps the entry", &remoteErr{code: 502, text: "gateway"}, 2},
		{"a transport error keeps the entry", errors.New("connection reset"), 2},
		{"not modified is a success", &remoteErr{code: 400, text: "message is not modified"}, 2},
	}
	for _, tt := range drops {
		t.Run(tt.name, func(t *testing.T) {
			rec, fw := &recorder{}, &fakeWait{}
			rs := &results{}
			c := newC(t, Config{Wait: fw.wait, Errors: codeReader{}, Hooks: Hooks{Edited: rs.add}})
			first := true
			ed := editFunc(func(ctx context.Context, object, data string) error {
				_ = rec.Edit(ctx, object, data)
				if first {
					first = false
					c.Put(rec, "bot1", "tag", "W", "during")
					return tt.err
				}
				return nil
			})
			c.Put(ed, "bot1", "tag", "W", "x")
			c.Wait()
			if n := len(rec.got()); n != tt.wantEdits {
				t.Errorf("edits = %d, want %d", n, tt.wantEdits)
			}
			if n := c.Len(); n != 0 {
				t.Errorf("%d entries live, want none", n)
			}
			if got := rs.all(); len(got) != tt.wantEdits {
				t.Errorf("Edited called %d times, want once per edit sequence (%d)", len(got), tt.wantEdits)
			}
		})
	}

	t.Run("a nil Errors reads every error as a plain failure", func(t *testing.T) {
		rec, fw := &recorder{errs: []error{&remoteErr{code: 429}, &remoteErr{code: 400}}}, &fakeWait{}
		c := newC(t, Config{Wait: fw.wait})
		c.Put(rec, "bot1", "tag", "W", "a")
		c.Wait()
		if n := len(rec.got()); n != 1 {
			t.Errorf("edits = %d, want 1 (no retry, no hold)", n)
		}
		if got := fw.take(); !slices.Equal(got, []time.Duration{DefaultWindow}) {
			t.Errorf("waits = %v, want one window", got)
		}
	})

	t.Run("the live objects are bounded and the overflow is reported once per interval", func(t *testing.T) {
		type full struct {
			tag            string
			limit, dropped int
		}
		var (
			mu    sync.Mutex
			fulls []full
		)
		rec, g, clk := &recorder{}, newGate(), newClock()
		c := newC(t, Config{MaxEntries: 3, Now: clk.Now, Wait: g.wait, Hooks: Hooks{Full: func(tag string, limit, dropped int) {
			mu.Lock()
			defer mu.Unlock()
			fulls = append(fulls, full{tag, limit, dropped})
		}}})
		for i := range 3 {
			c.Put(rec, "bot1", "tag", fmt.Sprint("W", i), "x")
		}
		c.Put(rec, "bot1", "t1", "over1", "x")
		c.Put(rec, "bot1", "t2", "over2", "x")
		if n := c.Len(); n != 3 {
			t.Fatalf("live = %d, want 3", n)
		}
		c.Put(rec, "bot1", "tag", "W0", "again") // a live object still takes its data
		clk.Add(DefaultOverflowEvery)
		c.Put(rec, "bot1", "t3", "over3", "x")
		close(g.release)
		c.Wait()
		mu.Lock()
		defer mu.Unlock()
		// The first drop is reported at once (the clock is far past the zero lastFull); the second is counted, the third
		// reported a minute later with the one dropped in between.
		if want := []full{{"t1", 3, 1}, {"t3", 3, 2}}; !slices.Equal(fulls, want) {
			t.Errorf("Full calls = %+v, want %+v", fulls, want)
		}
		if n := len(rec.got()); n != 3 {
			t.Errorf("edits = %d, want 3 (the dropped ones never go out)", n)
		}
	})

	t.Run("Edited reports the tag, the object, the time taken and the error", func(t *testing.T) {
		clk, fw, rs := newClock(), &fakeWait{}, &results{}
		c := newC(t, Config{Now: clk.Now, Wait: fw.wait, Errors: codeReader{}, Hooks: Hooks{Edited: rs.add}})
		var fail error
		ed := editFunc(func(context.Context, string, string) error {
			clk.Add(7 * time.Millisecond)
			return fail
		})
		c.Put(ed, "bot1", "pub", "W", "x")
		c.Wait()
		fail = &remoteErr{code: 400, text: "nope"}
		c.Put(ed, "bot1", "pub", "W", "y")
		c.Wait()
		got := rs.all()
		if len(got) != 2 {
			t.Fatalf("results = %+v, want 2", got)
		}
		ok, bad := got[0], got[1]
		if ok.Tag != "pub" || ok.Object != "W" || !ok.OK || ok.Took != 7*time.Millisecond || ok.Err != nil {
			t.Errorf("ok result = %+v", ok)
		}
		if bad.OK || bad.Err == nil || bad.Took != 7*time.Millisecond {
			t.Errorf("failed result = %+v", bad)
		}
	})
}

func TestClose(t *testing.T) {
	bg := context.Background()

	t.Run("close sends each waiting entry once and later puts do nothing", func(t *testing.T) {
		rec, g := &recorder{}, newGate()
		c := New(bg, Config{Wait: g.wait})
		c.Put(rec, "bot1", "tag", "W1", "a")
		c.Put(rec, "bot1", "tag", "W2", "b")
		c.Put(rec, "bot1", "tag", "W1", "c")
		g.await(t)
		g.await(t)
		c.Close(bg)
		got := rec.got()
		slices.SortFunc(got, func(a, b call) int { return strings.Compare(a.object, b.object) })
		if want := []call{{"W1", "c"}, {"W2", "b"}}; !slices.Equal(got, want) {
			t.Errorf("edits = %+v, want one per object with its last data", got)
		}
		c.Put(rec, "bot1", "tag", "W3", "late")
		c.Close(bg) // a second Close is a no-op
		c.Wait()
		if n := len(rec.got()); n != 2 {
			t.Errorf("edits after close = %d, want 2", n)
		}
	})

	t.Run("a flood wait is not waited out while closing", func(t *testing.T) {
		rec, g := &recorder{errs: []error{flood(30 * time.Second)}}, newGate()
		c := New(bg, Config{Wait: g.wait, Errors: codeReader{}})
		c.Put(rec, "bot1", "tag", "W", "a")
		g.await(t)
		c.Close(bg)
		if n := len(rec.got()); n != 1 {
			t.Errorf("edits = %d, want one attempt", n)
		}
	})

	// blocked is an editor whose W1 edit completes and whose others hold until unblock, ignoring their context: a stuck
	// call that only the bound can end.
	blocked := func() (editFunc, chan string, chan struct{}) {
		started, unblock := make(chan string, 4), make(chan struct{})
		return func(_ context.Context, object, _ string) error {
			started <- object
			if object != "W1" {
				<-unblock
			}
			return nil
		}, started, unblock
	}

	t.Run("the bound drops what is left and reports it", func(t *testing.T) {
		var dropped []int
		var mu sync.Mutex
		ed, started, unblock := blocked()
		g := newGate()
		c := New(bg, Config{Wait: g.wait, Hooks: Hooks{Dropped: func(n int) {
			mu.Lock()
			defer mu.Unlock()
			dropped = append(dropped, n)
		}}})
		for _, id := range []string{"W1", "W2", "W3"} {
			c.Put(ed, "bot1", "tag", id, "x")
			g.await(t)
		}
		done := make(chan struct{})
		go func() {
			c.Close(bg)
			close(done)
		}()
		for range 3 {
			<-started
		}
		eventually(t, "W1's entry to leave", func() bool { return c.Len() == 2 })
		for bound := false; !bound; {
			bound = g.await(t) == DefaultCloseBound
		}
		close(g.release) // the bound passes
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Close did not return at its bound")
		}
		close(unblock)
		c.Wait()
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(dropped, []int{2}) {
			t.Errorf("Dropped calls = %v, want one counting 2", dropped)
		}
	})

	t.Run("the context bounds close too", func(t *testing.T) {
		var dropped []int
		var mu sync.Mutex
		ed, started, unblock := blocked()
		g := newGate()
		c := New(bg, Config{Wait: g.wait, Hooks: Hooks{Dropped: func(n int) {
			mu.Lock()
			defer mu.Unlock()
			dropped = append(dropped, n)
		}}})
		for _, id := range []string{"W2", "W3"} {
			c.Put(ed, "bot1", "tag", id, "x")
			g.await(t)
		}
		ctx, cancel := context.WithCancel(bg)
		cancel()
		c.Close(ctx)
		close(unblock)
		c.Wait()
		for range 2 {
			<-started
		}
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(dropped, []int{2}) {
			t.Errorf("Dropped calls = %v, want one counting 2", dropped)
		}
	})
}
