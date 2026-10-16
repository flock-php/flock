package binaries

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// CheckInterval is how old the manifest may get before it is fetched again.
var CheckInterval = 6 * time.Hour

// Catalog is the daemon's one copy of the manifest of the channel it follows: loaded from the
// cache at once, fetched soon after start and then whenever it is older than CheckInterval, and
// again when the channel changes. Every package manager reads it from here.
type Catalog struct {
	source  Source
	channel func() Channel // the channel to follow (from config.json)

	mu         sync.Mutex
	loaded     Channel
	manifest   *Manifest
	checkedAt  time.Time
	checkError string
	checking   atomic.Bool
	onChange   []func()
}

// Status is what the catalog knows, for the UIs.
type Status struct {
	Channel    Channel    `json:"channel"`
	CheckedAt  *time.Time `json:"checked_at"`
	CheckError string     `json:"check_error"`
}

// NewCatalog follows the channel channel() returns.
func NewCatalog(source Source, channel func() Channel) *Catalog {
	return &Catalog{source: source, channel: channel}
}

// OnChange registers f to run (in the background) after each fetch that changed the manifest.
func (c *Catalog) OnChange(f func()) {
	c.mu.Lock()
	c.onChange = append(c.onChange, f)
	c.mu.Unlock()
}

// sync switches to the configured channel when it differs from the one loaded, starting from the
// channel's cached manifest. c.mu must be held; it reports whether the channel changed.
func (c *Catalog) sync() bool {
	ch := c.channel()
	if ch == c.loaded {
		return false
	}
	c.loaded, c.manifest, c.checkedAt, c.checkError = ch, c.source.Cached(ch), time.Time{}, ""
	return true
}

// Manifest is the current manifest (nil before the first successful fetch with no cache). It
// doesn't wait on the network: a stale one is fetched in the background.
func (c *Catalog) Manifest() *Manifest {
	c.mu.Lock()
	switched := c.sync()
	m, stale := c.manifest, c.checkedAt.IsZero() || time.Since(c.checkedAt) > CheckInterval
	c.mu.Unlock()
	if switched || stale {
		c.CheckInBackground()
	}
	return m
}

// Status reports the channel and the last fetch.
func (c *Catalog) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sync()
	st := Status{Channel: c.loaded, CheckError: c.checkError}
	if !c.checkedAt.IsZero() {
		at := c.checkedAt
		st.CheckedAt = &at
	}
	return st
}

// Check fetches the manifest of the configured channel now.
func (c *Catalog) Check(ctx context.Context) (*Manifest, error) {
	c.mu.Lock()
	c.sync()
	ch := c.loaded
	c.mu.Unlock()

	m, err := c.source.Fetch(ctx, ch)

	c.mu.Lock()
	if c.loaded != ch { // the channel changed meanwhile: this result is for the old one
		c.mu.Unlock()
		return m, err
	}
	c.checkedAt = time.Now().UTC()
	if err != nil {
		c.checkError = err.Error()
		c.mu.Unlock()
		return nil, err
	}
	changed := c.manifest == nil || c.manifest.Serial != m.Serial
	c.manifest, c.checkError = m, ""
	hooks := append([]func(){}, c.onChange...)
	c.mu.Unlock()
	if changed {
		for _, f := range hooks {
			go f()
		}
	}
	return m, nil
}

// CheckInBackground fetches the manifest unless a fetch is running.
func (c *Catalog) CheckInBackground() {
	if !c.checking.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.checking.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = c.Check(ctx)
	}()
}

// ChannelChanged picks up a changed channel in config.json and fetches its manifest.
func (c *Catalog) ChannelChanged() {
	c.mu.Lock()
	switched := c.sync()
	c.mu.Unlock()
	if switched {
		c.CheckInBackground()
	}
}

// Run keeps the manifest fresh until ctx ends: soon after start, then looked at hourly (so a Mac
// that slept catches up) and fetched when older than CheckInterval.
func (c *Catalog) Run(ctx context.Context) {
	if Platform() == "" {
		return
	}
	c.mu.Lock()
	c.sync()
	c.mu.Unlock()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			c.mu.Lock()
			c.sync()
			stale := c.checkedAt.IsZero() || time.Since(c.checkedAt) > CheckInterval
			c.mu.Unlock()
			if stale {
				checkCtx, cancel := context.WithTimeout(ctx, time.Minute)
				_, _ = c.Check(checkCtx)
				cancel()
			}
			timer.Reset(time.Hour)
		}
	}
}
