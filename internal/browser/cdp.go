package browser

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

type reply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// message is a command to Chrome.
type message struct {
	ID        int64  `json:"id"`
	Method    string `json:"method"`
	SessionID string `json:"sessionId,omitempty"`
	Params    any    `json:"params,omitempty"`
}

// incoming is a reply or an event from Chrome.
type incoming struct {
	ID        int64           `json:"id"`
	Method    string          `json:"method"`
	SessionID string          `json:"sessionId"`
	Params    json.RawMessage `json:"params"`
	Result    json.RawMessage `json:"result"`
	Error     *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// read delivers replies to their callers and events to event.
func (b *Browser) read(r io.ReadCloser) {
	defer func() { _ = r.Close() }()
	br := bufio.NewReaderSize(r, 1<<20)
	var err error
	for {
		var raw []byte
		raw, err = br.ReadBytes(0)
		if err != nil {
			break
		}
		var m incoming
		if json.Unmarshal(bytes.TrimSuffix(raw, []byte{0}), &m) != nil {
			continue
		}
		if m.ID == 0 {
			b.event(m)
			continue
		}
		b.mu.Lock()
		ch := b.pending[m.ID]
		delete(b.pending, m.ID)
		b.mu.Unlock()
		if ch != nil {
			ch <- reply{Result: m.Result, Error: m.Error}
		}
	}
	b.mu.Lock()
	b.readErr = fmt.Errorf("browser: Chrome closed the connection: %w", err)
	for id, ch := range b.pending {
		close(ch)
		delete(b.pending, id)
	}
	b.mu.Unlock()
}

// event handles the events Start asked for: requests paused for the proxy
// login go on, and the proxy's own challenge gets the proxy's credentials
// (a website's gets none). Replies are sent without waiting, as read, which
// would deliver them, is the caller.
func (b *Browser) event(m incoming) {
	var p struct {
		RequestID     string `json:"requestId"`
		AuthChallenge struct {
			Source string `json:"source"`
		} `json:"authChallenge"`
	}
	switch m.Method {
	case "Fetch.requestPaused":
		if json.Unmarshal(m.Params, &p) == nil {
			b.send(m.SessionID, "Fetch.continueRequest", map[string]any{"requestId": p.RequestID})
		}
	case "Fetch.authRequired":
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		answer := map[string]any{"response": "CancelAuth"}
		if p.AuthChallenge.Source == "Proxy" && b.proxyAuth != nil {
			pw, _ := b.proxyAuth.Password()
			answer = map[string]any{"response": "ProvideCredentials", "username": b.proxyAuth.Username(), "password": pw}
		}
		b.send(m.SessionID, "Fetch.continueWithAuth", map[string]any{"requestId": p.RequestID, "authChallengeResponse": answer})
	}
}

// send sends a command without waiting for its result.
func (b *Browser) send(session, method string, params any) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = b.call(ctx, session, method, params)
	}()
}

// call sends one command (to a page if session is set) and waits for its
// result.
func (b *Browser) call(ctx context.Context, session, method string, params any) (json.RawMessage, error) {
	b.mu.Lock()
	if b.closed || b.readErr != nil {
		err := b.readErr
		b.mu.Unlock()
		if err == nil {
			err = errors.New("browser: closed")
		}
		return nil, err
	}
	b.nextID++
	id := b.nextID
	ch := make(chan reply, 1)
	b.pending[id] = ch
	msg, err := json.Marshal(message{ID: id, Method: method, SessionID: session, Params: params})
	if err == nil {
		_, err = b.in.Write(append(msg, 0))
	}
	b.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("browser: %s: %w", method, err)
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("browser: %s: Chrome went away", method)
		}
		if r.Error != nil {
			return nil, fmt.Errorf("browser: %s: %s", method, r.Error.Message)
		}
		return r.Result, nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, ctx.Err()
	}
}
