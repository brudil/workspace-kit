// Package rpc is one side of a JSON-RPC 2.0 connection over a pair of
// streams, one message per line: what a plugin and ws speak. Both the
// framework and its test harness use it.
package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/withworkspace/plugin/protocol"
)

// message is any JSON-RPC message: a request (ID and Method), a
// notification (Method alone) or a response (ID, and Result or Error).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC error.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

// MethodNotFound is the answer to a request nobody handles.
func MethodNotFound(method string) *Error {
	return &Error{Code: protocol.CodeMethodNotFound, Message: "method not found: " + method}
}

// Handler answers what the other side asks: requests (whose answer is the
// result, or an error) and notifications (whose answer is dropped). ctx ends
// when the connection closes, or when the other side gives up on the
// request ($/cancel_request).
//
// A notification is handled before anything after it is, so its handler
// mustn't wait on a call of its own: the answer is queued behind it.
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

// Then is a request's result with something to do once it's been sent: a
// handler returns it to act only after the other side has its answer.
type Then struct {
	Result any
	Do     func()
}

// inbound is a request or notification as it came, with the context its
// handler runs in.
type inbound struct {
	m   message
	ctx context.Context
}

// Conn is one side of a connection.
type Conn struct {
	w       io.Writer
	handler Handler
	// ctx ends when the connection closes, taking every handler's with it.
	ctx    context.Context
	cancel context.CancelFunc

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int
	waiting map[string]chan message       // our requests, by id
	serving map[string]context.CancelFunc // theirs being answered, by id
	ended   bool
	err     error
	closed  chan struct{}
	// incoming is what the other side sends, handled in the order it came.
	incoming chan inbound
}

// NewConn reads messages from r until it ends, writing to w; handler
// answers what comes in. The connection is closed (Done) when r ends.
func NewConn(r io.Reader, w io.Writer, handler Handler) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		w: w, handler: handler, ctx: ctx, cancel: cancel,
		waiting: map[string]chan message{}, serving: map[string]context.CancelFunc{},
		closed: make(chan struct{}), incoming: make(chan inbound, 4096),
	}
	go c.dispatch()
	go c.read(r)
	return c
}

// dispatch handles what the other side sends, in order. A notification is
// handled, and an answer handed to its caller, before the next message; a
// request gets its own goroutine once everything before it has been
// handled, so a slow one holds nothing else up.
func (c *Conn) dispatch() {
	for in := range c.incoming {
		switch {
		case in.m.Method == "":
			c.mu.Lock()
			ch := c.waiting[string(in.m.ID)]
			delete(c.waiting, string(in.m.ID))
			c.mu.Unlock()
			if ch != nil {
				ch <- in.m
			}
		case len(in.m.ID) > 0:
			go c.answer(in)
		default:
			c.handler(in.ctx, in.m.Method, in.m.Params) //nolint:errcheck // a notification has no answer
		}
	}
	// Done before any call is failed, so a caller told ErrClosed finds the
	// connection done.
	c.mu.Lock()
	c.ended = true
	close(c.closed)
	for id, ch := range c.waiting {
		close(ch)
		delete(c.waiting, id)
	}
	c.mu.Unlock()
	c.cancel()
}

// Done is closed once the other side's gone: its stream ended.
func (c *Conn) Done() <-chan struct{} { return c.closed }

// Err is why the connection closed (nil for its stream simply ending).
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// ErrClosed is a request's answer once the connection's gone.
var ErrClosed = errors.New("the connection closed")

func (c *Conn) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20) // lines can be long
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			continue // not a message: skip it
		}
		switch {
		case m.Method == protocol.MethodCancelRequest:
			// Handled here, not queued: the request it's about may be what
			// everything queued is waiting behind.
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(m.Params, &p) == nil {
				c.mu.Lock()
				if cancel := c.serving[string(p.RequestID)]; cancel != nil {
					cancel()
				}
				c.mu.Unlock()
			}
		case m.Method != "":
			in := inbound{m: m, ctx: c.ctx}
			if len(m.ID) > 0 {
				// Registered as it's read, so a cancel that follows at once
				// finds it.
				ctx, cancel := context.WithCancel(c.ctx)
				in.ctx = ctx
				c.mu.Lock()
				c.serving[string(m.ID)] = cancel
				c.mu.Unlock()
			}
			c.incoming <- in
		case len(m.ID) > 0:
			c.incoming <- inbound{m: m} // an answer, in its place
		}
	}
	c.mu.Lock()
	c.err = sc.Err()
	c.mu.Unlock()
	close(c.incoming)
}

func (c *Conn) answer(in inbound) {
	result, err := c.handler(in.ctx, in.m.Method, in.m.Params)
	cancelled := in.ctx.Err() != nil
	c.mu.Lock()
	if cancel := c.serving[string(in.m.ID)]; cancel != nil {
		cancel()
		delete(c.serving, string(in.m.ID))
	}
	c.mu.Unlock()
	var then func()
	if t, ok := result.(Then); ok {
		result, then = t.Result, t.Do
	}
	reply := message{JSONRPC: "2.0", ID: in.m.ID}
	if err != nil {
		var re *Error
		switch {
		case errors.As(err, &re):
		case cancelled || errors.Is(err, context.Canceled):
			re = &Error{Code: protocol.CodeCancelled, Message: "Request cancelled"}
		default:
			re = &Error{Code: protocol.CodeInternal, Message: err.Error()}
		}
		reply.Error = re
	} else {
		data, merr := json.Marshal(result)
		if merr != nil {
			reply.Error = &Error{Code: protocol.CodeInternal, Message: merr.Error()}
		} else {
			reply.Result = data
		}
	}
	c.write(reply) //nolint:errcheck // nothing to do if the other side's gone
	if then != nil {
		then()
	}
}

func (c *Conn) write(m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.w.Write(append(data, '\n'))
	return err
}

// Call sends a request and waits for its answer, decoded into result (if
// not nil). If ctx ends first, it tells the other side it's given up
// ($/cancel_request) and returns ctx's error.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return ErrClosed
	}
	c.nextID++
	id := json.RawMessage(strconv.Itoa(c.nextID))
	ch := make(chan message, 1)
	c.waiting[string(id)] = ch
	c.mu.Unlock()
	if err := c.write(message{JSONRPC: "2.0", ID: id, Method: method, Params: data}); err != nil {
		c.mu.Lock()
		delete(c.waiting, string(id))
		c.mu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.waiting, string(id))
		c.mu.Unlock()
		c.Notify(protocol.MethodCancelRequest, map[string]json.RawMessage{"requestId": id}) //nolint:errcheck // a courtesy
		return ctx.Err()
	case m, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if m.Error != nil {
			return m.Error
		}
		if result != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	}
}

// Notify sends a notification: no answer.
func (c *Conn) Notify(method string, params any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(message{JSONRPC: "2.0", Method: method, Params: data})
}
