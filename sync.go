package plugin

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/withworkspace/plugin/protocol"
)

// syncer is one sync: what it does, how often, and how it last went.
type syncer struct {
	p     *Plugin
	name  string
	every time.Duration // between runs: 0 is no interval of its own
	open  time.Duration // between runs while the app's on screen: 0 is as every
	fn    func(context.Context) error

	mu       sync.Mutex
	running  chan struct{} // closed when the run in flight ends
	at       time.Time     // when it last succeeded
	ran      time.Time     // when it last ended, however it went, by the wall clock
	took     time.Duration
	err      error         // how the last run went
	failures int           // in a row
	wake     chan struct{} // config changed
	look     chan struct{} // who's looking changed
}

// SyncOption says more of when a sync runs.
type SyncOption func(*syncer)

// WhileOpen is how often a sync runs while the plugin's app is on screen
// (Looking().App), in place of its own interval. When the app comes on
// screen, the sync runs at once if its last run is further back than
// this, and otherwise when it is.
func WhileOpen(every time.Duration) SyncOption {
	return func(s *syncer) { s.open = every }
}

// SyncState is how a sync stands: for ws plugin list, and for a plugin
// whose app shows it.
type SyncState struct {
	Name    string
	At      time.Time // when it last succeeded: zero if it hasn't
	Took    time.Duration
	Error   string // why the last run failed, or ""
	Running bool
}

// retryAfter is the wait after the first failure in a row; each failure
// after doubles it, up to the sync's own interval.
var retryAfter = 10 * time.Second

// Sync declares a sync: the one way a plugin talks to its service. fn
// fetches, and writes what it finds to the plugin's database; the app's
// API, commands and everything else read from there, so nothing a person
// or an agent waits on is as slow as the service.
//
// It runs when the plugin starts, every `every` after, and when asked
// (ws <plugin> sync, or SyncNow). One runs at a time: a second request
// waits for the first's result. A failure is tried again sooner, with a
// growing wait, and what was fetched before stays. It waits while
// required config is missing. When it succeeds, the open app is told
// (Changed).
//
// When it runs is the plugin's to say: ws tells a plugin who's looking
// (Looking), and never asks for a run. An `every` of 0 is a sync with no
// interval of its own, which runs at start, when asked, and as its
// options say: with WhileOpen, only while the plugin's app is on screen.
// A run is due by the wall clock, so a machine that's slept through one
// runs it when it wakes.
func (p *Plugin) Sync(name string, every time.Duration, fn func(ctx context.Context) error, opts ...SyncOption) {
	p.declaring("Sync")
	for _, was := range p.syncs {
		if was.name == name {
			p.declErrs = append(p.declErrs, fmt.Errorf("sync %q is declared twice", name))
		}
	}
	s := &syncer{p: p, name: name, every: every, fn: fn, wake: make(chan struct{}, 1), look: make(chan struct{}, 1)}
	for _, opt := range opts {
		opt(s)
	}
	if s.every < 0 || s.open < 0 {
		p.declErrs = append(p.declErrs, fmt.Errorf("sync %q has an interval below 0", name))
	}
	p.syncs = append(p.syncs, s)
}

// startSyncs starts each sync's loop, once ws has the registration.
func (p *Plugin) startSyncs() {
	for _, s := range p.syncs {
		go s.loop()
	}
}

// wakeSyncs has waiting syncs look again: config changed.
func (p *Plugin) wakeSyncs() {
	for _, s := range p.syncs {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// ready is whether syncs may run: config's complete and the database
// came up.
func (p *Plugin) ready() bool { return p.broken == nil && p.Config.complete() }

// lookSyncs has each sync work out again when it's next due: who's
// looking changed.
func (p *Plugin) lookSyncs() {
	for _, s := range p.syncs {
		select {
		case s.look <- struct{}{}:
		default:
		}
	}
}

// loop runs the sync at start, and then whenever its next run is due,
// until the plugin stops.
func (s *syncer) loop() {
	for {
		if s.p.ready() {
			s.run(s.p.ctx) //nolint:errcheck // how it went is kept, and says how long it rests
		}
		if !s.rest() {
			return
		}
	}
}

// wait is how long the sync rests after a run: its interval, as who's
// looking has it, or less after a failure. 0 is until something changes:
// it has no interval as things stand, or config's missing.
func (s *syncer) wait() time.Duration {
	if !s.p.ready() {
		return 0
	}
	every := s.every
	if s.open > 0 && s.p.Looking().App {
		every = s.open
	}
	s.mu.Lock()
	n := s.failures
	s.mu.Unlock()
	if n > 0 {
		every = min(every, retryAfter<<min(n-1, 16))
	}
	return every
}

// rest waits until the sync's next run is due, and says whether the
// plugin's still running. How long that is follows who's looking, so
// it's worked out again when ws says that's changed. It's counted from
// the last run by the wall clock, which is looked at again every
// recheck: a timer doesn't count the time a machine sleeps, and the
// clock does. A change of config ends the wait early for a sync that was
// waiting on config, or whose last run failed: the change may be what it
// needed.
func (s *syncer) rest() bool {
	spread := rand.Float64() / 10 // so plugins don't all fetch at once
	for {
		wait := s.wait()
		var timer *time.Timer
		var due <-chan time.Time
		if wait > 0 {
			wait += time.Duration(float64(wait) * spread)
			s.mu.Lock()
			left := s.ran.Add(wait).Sub(s.p.now())
			if left > wait {
				// The clock's been put back to before the last run: the
				// wait is counted from now, not made longer by it.
				s.ran, left = s.p.now(), wait
			}
			s.mu.Unlock()
			if left <= 0 {
				return true
			}
			timer = time.NewTimer(min(left, s.p.recheck))
			due = timer.C
		}
		again := false
		select {
		case <-s.p.ctx.Done():
		case <-due:
			again = true
		case <-s.look:
			again = true
		case <-s.wake:
			s.mu.Lock()
			failed := s.err != nil
			s.mu.Unlock()
			if wait == 0 || failed {
				return true
			}
			again = true
		}
		if timer != nil {
			timer.Stop()
		}
		if !again {
			return false
		}
	}
}

// run runs the sync, or waits for the run in flight, and returns how it
// went.
func (s *syncer) run(ctx context.Context) error {
	s.mu.Lock()
	if running := s.running; running != nil {
		s.mu.Unlock()
		select {
		case <-running:
		case <-ctx.Done():
			return ctx.Err()
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.err
	}
	done := make(chan struct{})
	s.running = done
	s.mu.Unlock()

	// The run is the plugin's, not the asker's: someone giving up on a
	// request doesn't stop a fetch that others are waiting on.
	runCtx, cancel := context.WithTimeout(s.p.ctx, s.p.SyncTimeout)
	start := time.Now()
	err := s.p.recovering("sync "+s.name, func() error { return s.fn(runCtx) })
	cancel()
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("ran out of time (%s)", s.p.SyncTimeout)
	}
	took := time.Since(start)

	s.mu.Lock()
	s.running, s.took, s.err, s.ran = nil, took, err, s.p.now()
	if err == nil {
		s.at, s.failures = time.Now(), 0
	} else {
		s.failures++
	}
	s.mu.Unlock()
	close(done)

	state := protocol.SyncStateParams{Name: s.name, At: time.Now().UTC().Format(time.RFC3339), TookMS: took.Milliseconds()}
	if err != nil {
		state.Error = err.Error()
		if s.p.ctx.Err() == nil {
			s.p.Log.Warn(fmt.Sprintf("sync %s: %v", s.name, err))
		}
	}
	s.p.notify(protocol.MethodSyncState, state)
	if err == nil {
		s.p.Changed()
	}
	return err
}

func (s *syncer) state() SyncState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := SyncState{Name: s.name, At: s.at, Took: s.took, Running: s.running != nil}
	if s.err != nil {
		st.Error = s.err.Error()
	}
	return st
}

// SyncStates is how each sync stands.
func (p *Plugin) SyncStates() []SyncState {
	out := make([]SyncState, 0, len(p.syncs))
	for _, s := range p.syncs {
		out = append(out, s.state())
	}
	return out
}

// errNoConfig is why nothing syncs while required config is missing.
var errNoConfig = errors.New("the plugin's config isn't complete, so nothing was synced: ws plugin config sets it")

// SyncNow runs the named syncs now (all of them, given none), waiting
// for each, and returns what went wrong.
func (p *Plugin) SyncNow(ctx context.Context, names ...string) error {
	if !p.Config.complete() {
		return errNoConfig
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var errs []error
	for _, s := range p.syncs {
		if len(want) > 0 && !want[s.name] {
			continue
		}
		if err := s.run(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.name, err))
		}
	}
	return errors.Join(errs...)
}

// addSyncCommand gives a plugin with syncs its sync command, unless it
// declared one of its own.
func (p *Plugin) addSyncCommand() {
	if len(p.syncs) == 0 {
		return
	}
	for _, c := range p.commands {
		if c.name == "sync" {
			return
		}
	}
	p.commands = append(p.commands, &Command{
		name: "sync", short: "Fetch what's new now", args: "[NAME...]",
		fn: func(c *Call) error {
			for _, name := range c.Args() {
				known := false
				for _, s := range p.syncs {
					known = known || s.name == name
				}
				if !known {
					return Exit(2, fmt.Errorf("there's no sync called %q", name))
				}
			}
			err := p.SyncNow(c.Context(), c.Args()...)
			if errors.Is(err, errNoConfig) {
				return err
			}
			type row struct {
				Sync   string
				Synced string
				Took   string
				Error  string `json:",omitempty"`
			}
			var rows []row
			for _, s := range p.SyncStates() {
				r := row{Sync: s.Name, Synced: "never", Took: s.Took.Round(time.Millisecond).String(), Error: s.Error}
				if !s.At.IsZero() {
					r.Synced = relTime(s.At)
				}
				rows = append(rows, r)
			}
			if lerr := c.List(rows); lerr != nil {
				return lerr
			}
			if err != nil {
				return errors.New("not everything synced")
			}
			return nil
		},
	})
}
