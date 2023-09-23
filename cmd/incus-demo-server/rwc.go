package main

import (
	"io"
	"sync"

	"github.com/gorilla/websocket"
)

// wsWrapper implements ReadWriteCloser on top of a websocket connection.
type wsWrapper struct {
	conn   *websocket.Conn
	reader io.Reader
	mur    sync.Mutex
	muw    sync.Mutex
}

func (w *wsWrapper) Read(p []byte) (n int, err error) {
	w.mur.Lock()
	defer w.mur.Unlock()

	// Get new message if no active one.
	if w.reader == nil {
		var mt int

		mt, w.reader, err = w.conn.NextReader()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				return 0, io.EOF
			}

			return 0, err
		}

		if mt == websocket.CloseMessage {
			w.reader = nil // At the end of the message, reset reader.

			return 0, io.EOF
		}
	}

	// Perform the read itself.
	n, err = w.reader.Read(p)
	if err != nil {
		w.reader = nil // At the end of the message, reset reader.

		if err == io.EOF {
			return n, nil // Don't return EOF error at end of message.
		}

		return n, err
	}

	return n, nil
}

func (w *wsWrapper) Write(p []byte) (int, error) {
	w.muw.Lock()
	defer w.muw.Unlock()

	// Send the data as a text message.
	err := w.conn.WriteMessage(websocket.TextMessage, p)
	if err != nil {
		return 0, err
	}

	return len(p), nil
}
