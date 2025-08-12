//
// This file is part of pluggable-monitor-protocol-handler.
//
// Copyright 2025 ARDUINO SA (http://www.arduino.cc/)
//
// This software is released under the GNU General Public License version 3,
// which covers the main part of arduino-cli.
// The terms of this license can be found at:
// https://www.gnu.org/licenses/gpl-3.0.en.html
//
// You can be released from the requirements of the above licenses by purchasing
// a commercial license. Buying such a license is mandatory if you want to modify or
// otherwise use the software for commercial activities involving the Arduino
// software without disclosing the source code of your own applications. To purchase
// a commercial license, send an email to license@arduino.cc.
//

package monitor

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// BufferConfig controls how bytes coming from the board (port) are aggregated
// into larger chunks before being written to the TCP client. All fields are
// optional; invalid or zero values are normalized to sensible defaults via
// normalized().
type BufferConfig struct {
	HighWaterMark int           // >= 1
	FlushInterval time.Duration // 0 to disable time-based flush
	LineBuffering bool
	// FlushQueueCapacity controls how many aggregated payloads can be queued for the TCP writer.
	// If <= 0 a sensible default is applied.
	FlushQueueCapacity int
	// OverflowStrategy controls what to do if the queue is full when a flush happens.
	// Supported values:
	//   "drop"  (default)  -> drop the payload rather than blocking the port reader.
	//   "wait"            -> wait up to OverflowWait duration for space, then drop if still full.
	OverflowStrategy string
	// OverflowWait is only used when OverflowStrategy == "wait". If <= 0, the behavior falls back to "drop".
	OverflowWait time.Duration
}

func (c BufferConfig) normalized() BufferConfig {
	cfg := c
	if cfg.HighWaterMark < 1 {
		cfg.HighWaterMark = 1
	}
	if cfg.FlushQueueCapacity <= 0 {
		cfg.FlushQueueCapacity = 32
	}
	if cfg.OverflowStrategy == "" {
		cfg.OverflowStrategy = "drop"
	}
	if cfg.OverflowWait < 0 {
		cfg.OverflowWait = 0
	}
	return cfg
}

// Option is a functional option used to configure a Server at construction time.
type Option func(*Server)

// WithBufferConfig sets the Server's buffering behavior. The provided cfg is
// normalized (e.g., min values enforced) before being stored on the Server.
func WithBufferConfig(cfg BufferConfig) Option {
	return func(s *Server) {
		s.bufCfg = cfg.normalized()
	}
}

// internal aggregator used in port->conn pump
type aggregator struct {
	cfg    BufferConfig
	mu     sync.Mutex
	buf    bytes.Buffer
	timer  *time.Timer
	flushC chan []byte
	closed bool
}

func newAggregator(cfg BufferConfig) *aggregator {
	cfg = cfg.normalized()
	return &aggregator{
		cfg:    cfg,
		flushC: make(chan []byte, cfg.FlushQueueCapacity),
	}
}

func (a *aggregator) addChunk(p []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return
	}

	if len(p) == 0 {
		return
	}
	// append
	a.buf.Write(p)

	// schedule timer if needed and not running
	if a.cfg.FlushInterval > 0 && a.timer == nil {
		a.timer = time.AfterFunc(a.cfg.FlushInterval, a.onTimer)
	}

	// flush conditions
	if a.buf.Len() >= a.cfg.HighWaterMark || (a.cfg.LineBuffering && bytes.Contains(p, []byte{'\n'})) {
		a.flushLocked()
	}
}

func (a *aggregator) onTimer() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.flushLocked()
}

func (a *aggregator) flushLocked() {
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	if a.buf.Len() == 0 {
		return
	}
	out := make([]byte, a.buf.Len())
	copy(out, a.buf.Bytes())
	a.buf.Reset()
	switch a.cfg.OverflowStrategy {
	case "wait":
		// To avoid deadlocks, do not wait indefinitely while holding the mutex.
		// We allow a bounded wait; after that, drop.
		if a.cfg.OverflowWait <= 0 {
			select {
			case a.flushC <- out:
			default:
				// drop
			}
			return
		}
		deadline := time.NewTimer(a.cfg.OverflowWait)
		defer deadline.Stop()
		select {
		case a.flushC <- out:
		case <-deadline.C:
			// drop after bounded wait
		}
	default: // "drop"
		select {
		case a.flushC <- out:
		default:
			// drop if consumer is slow
		}
	}
}

func (a *aggregator) drainTo(w io.Writer) error {
	for payload := range a.flushC {
		if len(payload) == 0 {
			continue
		}
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func (a *aggregator) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	// Stop timer first to prevent future callbacks
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	// Flush any remaining data while the channel is still open
	a.flushLocked()
	// Now mark closed and close the channel
	a.closed = true
	close(a.flushC)
}
