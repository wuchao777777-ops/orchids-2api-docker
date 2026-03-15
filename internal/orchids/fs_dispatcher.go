package orchids

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"orchids-api/internal/upstream"
)

func beginOrchidsActiveWrite(state *requestState, path string) {
	if path == "" {
		return
	}
	if state.activeWrites == nil {
		state.activeWrites = make(map[string]*fileWriterState)
	}
	state.activeWrites[path] = &fileWriterState{path: path}
}

func appendOrchidsActiveWrite(state *requestState, path, text string) {
	if path == "" || text == "" || state.activeWrites == nil {
		return
	}
	if w, ok := state.activeWrites[path]; ok {
		w.buf.WriteString(text)
	}
}

func clearOrchidsActiveWrite(state *requestState, path string) {
	if path == "" || state.activeWrites == nil {
		return
	}
	delete(state.activeWrites, path)
}

func (c *Client) flushOrchidsActiveWrite(
	state *requestState,
	path string,
	onMessage func(upstream.SSEMessage),
	conn *websocket.Conn,
	wg *sync.WaitGroup,
	workdir string,
) {
	if path == "" || state.activeWrites == nil {
		return
	}
	w, ok := state.activeWrites[path]
	if !ok {
		return
	}
	c.dispatchFSOperation(map[string]interface{}{
		"operation": "write",
		"path":      path,
		"content":   w.buf.String(),
		"id":        fmt.Sprintf("stream_%d", time.Now().UnixMilli()),
	}, onMessage, conn, wg, workdir, nil)
	delete(state.activeWrites, path)
	state.hasFSOps = true
}

func (c *Client) dispatchFSOperation(
	msg map[string]interface{},
	onMessage func(upstream.SSEMessage),
	conn *websocket.Conn,
	wg *sync.WaitGroup,
	workdir string,
	rawData []byte,
) {
	onMessage(upstream.SSEMessage{
		Type:    "fs_operation",
		Event:   msg,
		Raw:     msg,
		RawJSON: cloneRawJSON(rawData),
	})
	wg.Add(1)
	go func(m map[string]interface{}) {
		defer wg.Done()
		if err := c.handleFSOperation(conn, m, func(success bool, data interface{}, errMsg string) {
			if onMessage != nil {
				onMessage(upstream.SSEMessage{
					Type: "fs_operation_result",
					Event: map[string]interface{}{
						"success": success,
						"data":    data,
						"error":   errMsg,
						"op":      m,
					},
				})
			}
		}, workdir); err != nil {
			// Error handled inside respond or logged via debug
		}
	}(msg)
}

func waitOrchidsFSOperations(ctx context.Context, state *requestState, wg *sync.WaitGroup, transport string) error {
	if !state.hasFSOps {
		return nil
	}

	fsDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(fsDone)
	}()

	select {
	case <-fsDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		slog.Warn("FS operations timed out in " + transport + " mode")
		return nil
	}
}
