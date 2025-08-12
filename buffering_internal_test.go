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
	"testing"
	"time"
)

// These tests exercise the aggregator's overflow behavior directly, without
// going through the TCP server. We intentionally do NOT start drainTo(), so
// the flush queue can fill up and trigger the configured strategy.

func Test_Aggregator_Overflow_Drop(t *testing.T) {
	cfg := BufferConfig{
		HighWaterMark:      1, // flush every byte
		FlushInterval:      0,
		LineBuffering:      false,
		FlushQueueCapacity: 1, // tiny queue to force overflow
		OverflowStrategy:   "drop",
	}
	a := newAggregator(cfg)

	// Push a burst of bytes quickly; with capacity=1 and no consumer, only the
	// first payload can be queued, the rest should be dropped.
	for i := 0; i < 50; i++ {
		a.addChunk([]byte{'a' + byte(i%26)})
	}
	// close will try a final flush; with a full queue and strategy=drop, it drops too.
	a.close()

	// Drain whatever remained in the queue and count.
	total := 0
	count := 0
	for p := range a.flushC {
		count++
		total += len(p)
	}

	if count > 1 { // capacity is 1, so at most 1 payload can remain queued
		t.Fatalf("expected at most 1 queued payload, got %d", count)
	}
	if total >= 50 { // we queued 50 bytes, but nearly all should be dropped
		t.Fatalf("expected drops with strategy=drop; total delivered=%d", total)
	}
}

func Test_Aggregator_Overflow_Wait(t *testing.T) {
	cfg := BufferConfig{
		HighWaterMark:      1,
		FlushInterval:      0,
		LineBuffering:      false,
		FlushQueueCapacity: 1,
		OverflowStrategy:   "wait",
		OverflowWait:       25 * time.Millisecond,
	}
	a := newAggregator(cfg)

	start := time.Now()
	for i := 0; i < 3; i++ {
		a.addChunk([]byte{'x'}) // first enqueues; next two wait ~OverflowWait then drop
	}
	a.close()
	elapsed := time.Since(start)

	// Drain queue and count payloads
	count := 0
	for range a.flushC {
		count++
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 queued payload with capacity=1 and no consumer; got %d", count)
	}

	// We called addChunk 3 times; with OverflowWait=25ms, we expect ~50ms waiting.
	// Allow slack for CI.
	if elapsed < 40*time.Millisecond {
		t.Fatalf("expected bounded waiting to take noticeable time; elapsed=%v (<40ms)", elapsed)
	}
}

func Test_Aggregator_QueueCapacity(t *testing.T) {
	cfg := BufferConfig{
		HighWaterMark:      1,
		FlushInterval:      0,
		LineBuffering:      false,
		FlushQueueCapacity: 3, // allow a few in-flight payloads
		OverflowStrategy:   "drop",
	}
	a := newAggregator(cfg)

	for i := 0; i < 10; i++ {
		a.addChunk([]byte{'A' + byte(i%26)})
	}
	a.close()

	count := 0
	for range a.flushC {
		count++
	}
	if count > 3 {
		t.Fatalf("expected at most queue capacity (3) payloads, got %d", count)
	}
}
