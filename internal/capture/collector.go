package capture

import (
	"bufio"
	"encoding/json"
	"errors"
	"log"
	"net"
	"slices"
	"sync"
	"time"

	"flock/internal/dumps"
)

// maxLine is the longest line the collector reads: a dump of a large structure renders big.
const maxLine = 64 << 20

// Collector receives the agent's JSON lines on a loopback TCP port and stores them.
type Collector struct {
	root string
	ln   net.Listener

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// Listen starts a collector on addr (e.g. "127.0.0.1:9913") that stores under root.
func Listen(root, addr string) (*Collector, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &Collector{root: root, ln: ln, conns: map[net.Conn]struct{}{}}
	go c.serve()
	return c, nil
}

// Addr is the address the collector listens on.
func (c *Collector) Addr() string { return c.ln.Addr().String() }

// Close stops listening and drops open connections.
func (c *Collector) Close() error {
	err := c.ln.Close()
	c.mu.Lock()
	for conn := range c.conns {
		_ = conn.Close()
	}
	c.mu.Unlock()
	return err
}

func (c *Collector) serve() {
	for {
		conn, err := c.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		c.mu.Lock()
		c.conns[conn] = struct{}{}
		c.mu.Unlock()
		go c.read(conn)
	}
}

// line is what the agent sends: an Event, or a dump with the fields of dumps.Entry.
type line struct {
	Event
	Label *string `json:"label"`
	Text  string  `json:"text"`
	HTML  string  `json:"html"`
}

func (c *Collector) read(conn net.Conn) {
	defer func() {
		c.mu.Lock()
		delete(c.conns, conn)
		c.mu.Unlock()
		_ = conn.Close()
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64<<10), maxLine)
	for scanner.Scan() {
		var l line
		if err := json.Unmarshal(scanner.Bytes(), &l); err != nil {
			continue
		}
		if err := c.store(l); err != nil {
			log.Printf("capture: %v", err)
		}
	}
}

func (c *Collector) store(l line) error {
	switch {
	case l.Kind == "dump":
		return dumps.Append(c.root, dumps.Entry{
			Time: l.Time, Label: l.Label, Source: l.Source, Request: l.Request, CommandLine: l.CommandLine,
			Group: l.Group, Text: l.Text, HTML: l.HTML,
		})
	case l.Kind == "dump_header":
		return dumps.SetHeader(c.root, l.HTML)
	case slices.Contains(Kinds, l.Kind):
		return Append(c.root, l.Event)
	}
	return nil // a kind from a newer agent
}
