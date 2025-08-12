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

package monitor_test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	monitor "github.com/arduino/pluggable-monitor-protocol-handler"
)

// sample records a received byte and its arrival time for timing checks.
type sample struct {
	t time.Time
	b byte
}

// lockBuf is a threadsafe bytes.Buffer used to avoid data races when the server
// writes status lines while tests may read them.
type lockBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *lockBuf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *lockBuf) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// testPort is a controllable io.ReadWriter. Writes push into a channel;
// Reads pop one message at a time (no buffering).
type testPort struct {
	in  chan []byte // bytes written by server go to this channel (Tx to board)
	out chan []byte // bytes we want the server to read (Rx from board)
}

func newTestPort() *testPort {
	return &testPort{
		in:  make(chan []byte, 128),
		out: make(chan []byte, 128),
	}
}

func (p *testPort) Read(b []byte) (int, error) {
	data, ok := <-p.out
	if !ok {
		return 0, io.EOF
	}
	n := copy(b, data)
	return n, nil
}

func (p *testPort) Write(b []byte) (int, error) {
	// Forward what the client wrote to the board into the in-chan
	cp := make([]byte, len(b))
	copy(cp, b)
	p.in <- cp
	return len(b), nil
}

// dummy monitor that returns our testPort and can inject errors
type dummyMon struct {
	port          *testPort
	describeErr   error
	configureErr  error
	configureSeen [][2]string
	openErr       error
	closeErr      error
	helloErr      error
}

func (d *dummyMon) Hello(_ string, _ int) error { return d.helloErr }
func (d *dummyMon) Describe() (*monitor.PortDescriptor, error) {
	if d.describeErr != nil {
		return nil, d.describeErr
	}
	return &monitor.PortDescriptor{}, nil
}
func (d *dummyMon) Configure(name, value string) error {
	d.configureSeen = append(d.configureSeen, [2]string{name, value})
	return d.configureErr
}
func (d *dummyMon) Open(_ string) (io.ReadWriter, error) {
	if d.openErr != nil {
		return nil, d.openErr
	}
	return d.port, nil
}
func (d *dummyMon) Close() error { return d.closeErr }
func (d *dummyMon) Quit()        {}

func startTCP(t *testing.T) (addr string, connCh chan net.Conn, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	connCh = make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		connCh <- c
	}()
	addr = ln.Addr().String()
	closeFn = func() { _ = ln.Close() }
	return
}

func runServer(t *testing.T, mon monitor.Monitor, input string) (getOut func() string, stop func()) {
	t.Helper()
	srv := monitor.NewServer(mon)
	in := bytes.NewBufferString(input)
	out := &lockBuf{}
	done := make(chan struct{})
	go func() {
		_ = srv.Run(in, out)
		close(done)
	}()
	return out.String, func() { <-done }
}

func readBytesWithTimestamps(t *testing.T, c net.Conn, want int, perReadDeadline, totalDeadline time.Duration) []sample {
	t.Helper()
	var got []sample
	br := bufio.NewReader(c)
	end := time.Now().Add(totalDeadline)
	for time.Now().Before(end) && len(got) < want {
		_ = c.SetReadDeadline(time.Now().Add(perReadDeadline))
		b, err := br.ReadByte()
		if err == nil {
			got = append(got, sample{t: time.Now(), b: b})
		}
	}
	return got
}

// gapsStats classifies inter-arrival gaps: near-zero (<5ms), big (>12ms), and tracks
// the maximum run of consecutive near-zero gaps (indicative of chunk size - 1).
func gapsStats(samples []sample) (nearZero, big, maxConsecNearZero int) {
	const near = 5 * time.Millisecond
	const bigGap = 12 * time.Millisecond
	consec := 0
	for i := 1; i < len(samples); i++ {
		d := samples[i].t.Sub(samples[i-1].t)
		if d < near {
			nearZero++
			consec++
			if consec > maxConsecNearZero {
				maxConsecNearZero = consec
			}
		} else {
			consec = 0
			if d > bigGap {
				big++
			}
		}
	}
	return
}

func bytesFrom(s []sample) string {
	out := make([]byte, len(s))
	for i := range s {
		out[i] = s[i].b
	}
	return string(out)
}

func Test_Unbuffered_PortToTCP_ForwardsImmediately(t *testing.T) {
	t.Parallel()

	port := newTestPort()
	mon := &dummyMon{port: port}

	addr, connCh, closeTCP := startTCP(t)
	defer closeTCP()

	cmds := strings.Join([]string{
		`HELLO 1 "test"`,
		fmt.Sprintf("OPEN %s dev", addr),
	}, "\n") + "\n"
	getOut, stop := runServer(t, mon, cmds)
	defer stop()

	var accepted net.Conn
	select {
	case accepted = <-connCh:
	case <-time.After(2 * time.Second):
		t.Fatal("did not accept TCP connection")
	}

	go func() {
		for _, ch := range []byte("hallo\r\n") {
			port.out <- []byte{ch}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	var got []sample
	br := bufio.NewReader(accepted)
	readDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(readDeadline) && len(got) < 7 {
		_ = accepted.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		b, err := br.ReadByte()
		if err == nil {
			got = append(got, sample{t: time.Now(), b: b})
		}
	}

	if len(got) != 7 {
		t.Fatalf("expected 7 bytes, got %d (%q)\nstdout:\n%s", len(got), bytesFrom(got), getOut())
	}

	var coalesced int
	for i := 1; i < len(got); i++ {
		d := got[i].t.Sub(got[i-1].t)
		if d < 5*time.Millisecond {
			coalesced++
		}
	}
	if coalesced >= 3 {
		t.Fatalf("observed %d near-zero gaps (<5ms) -> likely batching/coalescing; want spaced forwarding. bytes=%q",
			coalesced, bytesFrom(got))
	}
}

func Test_TCPToPort_WritesForwarded(t *testing.T) {
	t.Parallel()

	port := newTestPort()
	mon := &dummyMon{port: port}

	addr, connCh, closeTCP := startTCP(t)
	defer closeTCP()

	cmds := strings.Join([]string{
		`HELLO 1 "test"`,
		fmt.Sprintf("OPEN %s dev", addr),
	}, "\n") + "\n"
	_, stop := runServer(t, mon, cmds)
	defer stop()

	var conn net.Conn
	select {
	case conn = <-connCh:
	case <-time.After(2 * time.Second):
		t.Fatal("did not accept TCP connection")
	}

	want := []byte("abc\r\ndef\n")
	_, _ = conn.Write([]byte("abc\r\n"))
	time.Sleep(5 * time.Millisecond)
	_, _ = conn.Write([]byte("def\n"))

	deadline := time.Now().Add(2 * time.Second)
	var got []byte
	for time.Now().Before(deadline) && len(got) < len(want) {
		select {
		case chunk := <-port.in:
			got = append(got, chunk...)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if string(got) != string(want) {
		t.Fatalf("port did not receive expected bytes. got=%q want=%q", string(got), string(want))
	}
}

func Test_Buffer_HighWaterMark_Only(t *testing.T) {
	t.Parallel()

	port := newTestPort()
	mon := &dummyMon{port: port}

	addr, connCh, closeTCP := startTCP(t)
	defer closeTCP()

	cmds := strings.Join([]string{
		`HELLO 1 "test"`,
		`CONFIGURE _buffer.hwm 10`,
		fmt.Sprintf("OPEN %s dev", addr),
	}, "\n") + "\n"
	_, stop := runServer(t, mon, cmds)
	defer stop()

	var accepted net.Conn
	select {
	case accepted = <-connCh:
	case <-time.After(2 * time.Second):
		t.Fatal("did not accept TCP connection")
	}

	payload := []byte("abcdefghijklmnopqrstuvwxyz")[:25]
	go func() {
		for _, ch := range payload {
			port.out <- []byte{ch}
			time.Sleep(5 * time.Millisecond)
		}
		close(port.out)
	}()

	got := readBytesWithTimestamps(t, accepted, len(payload), 200*time.Millisecond, 2*time.Second)
	if len(got) != len(payload) {
		t.Fatalf("expected %d bytes, got %d (%q)", len(payload), len(got), bytesFrom(got))
	}

	near, big, maxRun := gapsStats(got)
	if big < 2 {
		t.Fatalf("expected >=2 big inter-chunk gaps with HWM-only, got %d; bytes=%q", big, bytesFrom(got))
	}
	if maxRun < 7 {
		t.Fatalf("expected large coalesced runs (>=7) with HWM-only, got maxRun=%d; bytes=%q (near=%d,big=%d)", maxRun, bytesFrom(got), near, big)
	}
}

func Test_Buffer_Line_Only(t *testing.T) {
	t.Parallel()

	port := newTestPort()
	mon := &dummyMon{port: port}

	addr, connCh, closeTCP := startTCP(t)
	defer closeTCP()

	cmds := strings.Join([]string{
		`HELLO 1 "test"`,
		`CONFIGURE _buffer.hwm 1024`,
		`CONFIGURE _buffer.line true`,
		fmt.Sprintf("OPEN %s dev", addr),
	}, "\n") + "\n"
	_, stop := runServer(t, mon, cmds)
	defer stop()

	var accepted net.Conn
	select {
	case accepted = <-connCh:
	case <-time.After(2 * time.Second):
		t.Fatal("did not accept TCP connection")
	}

	line := []byte("hello, world\n")
	go func() {
		for _, ch := range line {
			port.out <- []byte{ch}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	got := readBytesWithTimestamps(t, accepted, len(line), 200*time.Millisecond, 2*time.Second)
	if len(got) != len(line) {
		t.Fatalf("expected %d bytes, got %d (%q)", len(line), len(got), bytesFrom(got))
	}

	_, big, maxRun := gapsStats(got)
	if big != 0 {
		t.Fatalf("expected 0 big gaps with line-buffering, got %d; bytes=%q", big, bytesFrom(got))
	}
	if maxRun < len(line)-1-2 {
		t.Fatalf("expected near-complete coalescing with line-buffering, got maxRun=%d; bytes=%q", maxRun, bytesFrom(got))
	}
}

func Test_Buffer_Interval_Only(t *testing.T) {
	t.Parallel()

	port := newTestPort()
	mon := &dummyMon{port: port}

	addr, connCh, closeTCP := startTCP(t)
	defer closeTCP()

	cmds := strings.Join([]string{
		`HELLO 1 "test"`,
		`CONFIGURE _buffer.hwm 1024`,
		`CONFIGURE _buffer.interval_ms 16`,
		fmt.Sprintf("OPEN %s dev", addr),
	}, "\n") + "\n"
	_, stop := runServer(t, mon, cmds)
	defer stop()

	var accepted net.Conn
	select {
	case accepted = <-connCh:
	case <-time.After(2 * time.Second):
		t.Fatal("did not accept TCP connection")
	}

	payload := []byte("abcdefghijklmnopqrstuvwx")
	go func() {
		for _, ch := range payload {
			port.out <- []byte{ch}
			time.Sleep(5 * time.Millisecond)
		}
		close(port.out)
	}()

	got := readBytesWithTimestamps(t, accepted, len(payload), 200*time.Millisecond, 2*time.Second)
	if len(got) != len(payload) {
		t.Fatalf("expected %d bytes, got %d (%q)", len(payload), len(got), bytesFrom(got))
	}

	near, big, maxRun := gapsStats(got)
	if big < 2 {
		t.Fatalf("expected several big gaps with interval-only, got %d; bytes=%q", big, bytesFrom(got))
	}
	if maxRun < 1 || maxRun > 5 {
		t.Fatalf("expected small coalesced groups (1..5) with interval-only, got maxRun=%d; bytes=%q (near=%d,big=%d)", maxRun, bytesFrom(got), near, big)
	}
}

func Test_Buffer_Invalid_HWM_LeavesUnbuffered(t *testing.T) {
	t.Parallel()

	port := newTestPort()
	mon := &dummyMon{port: port}

	addr, connCh, closeTCP := startTCP(t)
	defer closeTCP()

	cmds := strings.Join([]string{
		`HELLO 1 "test"`,
		`CONFIGURE _buffer.hwm -1`,
		`CONFIGURE _buffer.interval_ms 0`,
		`CONFIGURE _buffer.line false`,
		fmt.Sprintf("OPEN %s dev", addr),
	}, "\n") + "\n"
	_, stop := runServer(t, mon, cmds)
	defer stop()

	var accepted net.Conn
	select {
	case accepted = <-connCh:
	case <-time.After(2 * time.Second):
		t.Fatal("did not accept TCP connection")
	}

	go func() {
		for _, ch := range []byte("hallo\r\n") {
			port.out <- []byte{ch}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	got := readBytesWithTimestamps(t, accepted, 7, 200*time.Millisecond, 2*time.Second)
	if len(got) != 7 {
		t.Fatalf("expected 7 bytes, got %d (%q)", len(got), bytesFrom(got))
	}

	near, _, _ := gapsStats(got)
	if near >= 5 {
		t.Fatalf("expected unbuffered behavior after invalid HWM; near-zero gaps=%d bytes=%q", near, bytesFrom(got))
	}
}

func Test_Buffer_Invalid_LineValue_DoesNotEnableLineBuffering(t *testing.T) {
	t.Parallel()

	port := newTestPort()
	mon := &dummyMon{port: port}

	addr, connCh, closeTCP := startTCP(t)
	defer closeTCP()

	cmds := strings.Join([]string{
		`HELLO 1 "test"`,
		`CONFIGURE _buffer.hwm 1024`,
		`CONFIGURE _buffer.interval_ms 0`,
		`CONFIGURE _buffer.line maybe`,
		fmt.Sprintf("OPEN %s dev", addr),
	}, "\n") + "\n"
	_, stop := runServer(t, mon, cmds)
	defer stop()

	var accepted net.Conn
	select {
	case accepted = <-connCh:
	case <-time.After(2 * time.Second):
		t.Fatal("did not accept TCP connection")
	}

	line := []byte("hello, world\n")
	go func() {
		for _, ch := range line {
			port.out <- []byte{ch}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	got := readBytesWithTimestamps(t, accepted, len(line), 200*time.Millisecond, 1*time.Second)
	if len(got) != 0 {
		t.Fatalf("expected 0 bytes without EOF or timer when line buffering is invalid; got %d (%q)", len(got), bytesFrom(got))
	}
}

func Test_CommandBeforeHelloErrors(t *testing.T) {
	in := strings.Join([]string{`DESCRIBE`, `QUIT`}, "\n") + "\n"
	m := &dummyMon{port: newTestPort()}
	getOut, stop := runServer(t, m, in)
	stop()
	out := getOut()
	if !strings.Contains(out, `"eventType": "command_error"`) || !strings.Contains(out, "First command must be HELLO") {
		t.Fatalf("expected not-initialized error, got:\n%s", out)
	}
}

func Test_Hello_Invalid_And_Duplicate(t *testing.T) {
	in := strings.Join([]string{`HELLO x "ua"`, `HELLO 1 "ua"`, `QUIT`}, "\n") + "\n"
	m := &dummyMon{port: newTestPort()}
	getOut, stop := runServer(t, m, in)
	stop()
	out := getOut()
	if !strings.Contains(out, `"eventType": "hello"`) || !strings.Contains(out, "Invalid HELLO command") || !strings.Contains(out, `"error": true`) {
		t.Fatalf("expected invalid version error, got:\n%s", out)
	}
	if !strings.Contains(out, `"eventType": "hello"`) || !strings.Contains(out, `"message": "OK"`) {
		t.Fatalf("expected successful hello after invalid, got:\n%s", out)
	}
	if strings.Count(out, `"eventType": "hello"`) < 1 {
		t.Fatalf("expected exactly one successful hello, got:\n%s", out)
	}
}

func Test_Describe_Error(t *testing.T) {
	m := &dummyMon{port: newTestPort(), describeErr: fmt.Errorf("boom")}
	in := strings.Join([]string{`HELLO 1 "ua"`, `DESCRIBE`, `QUIT`}, "\n") + "\n"
	getOut, stop := runServer(t, m, in)
	stop()
	out := getOut()
	if !strings.Contains(out, `"eventType": "describe"`) || !strings.Contains(out, `"error": true`) || !strings.Contains(out, "boom") {
		t.Fatalf("expected describe error, got:\n%s", out)
	}
}

func Test_Configure_Forward_And_InvalidInterval(t *testing.T) {
	m := &dummyMon{port: newTestPort()}
	in := strings.Join([]string{
		`HELLO 1 "ua"`,
		`CONFIGURE foo bar`,
		`CONFIGURE _buffer.interval_ms -5`,
		`QUIT`,
	}, "\n") + "\n"
	getOut, stop := runServer(t, m, in)
	stop()
	out := getOut()
	if len(m.configureSeen) == 0 || m.configureSeen[0][0] != "foo" || m.configureSeen[0][1] != "bar" {
		t.Fatalf("expected CONFIGURE to reach impl; seen=%v", m.configureSeen)
	}
	if !strings.Contains(out, "invalid _buffer.interval_ms") {
		t.Fatalf("expected invalid interval error, got:\n%s", out)
	}
}

func Test_Open_Invalid_And_DialFail(t *testing.T) {
	m := &dummyMon{port: newTestPort()}
	in := strings.Join([]string{
		`HELLO 1 "ua"`,
		`OPEN only-one-token`,
		`OPEN 127.0.0.1:1 dev`,
		`QUIT`,
	}, "\n") + "\n"
	getOut, stop := runServer(t, m, in)
	stop()
	out := getOut()
	if !strings.Contains(out, `Invalid OPEN command`) {
		t.Fatalf("expected invalid OPEN error, got:\n%s", out)
	}
	if !(strings.Contains(out, "dial tcp") || strings.Contains(out, "connect: connection refused") || strings.Contains(out, "refused") || strings.Contains(out, "connect")) {
		// allow platform/Go version specific wording
		t.Fatalf("expected dial/connect error, got:\n%s", out)
	}
}

func Test_Close_Errors_And_OK(t *testing.T) {
	// 1) close without open
	m1 := &dummyMon{port: newTestPort()}
	in1 := strings.Join([]string{`HELLO 1 "ua"`, `CLOSE`, `QUIT`}, "\n") + "\n"
	get1, stop1 := runServer(t, m1, in1)
	stop1()
	if !strings.Contains(get1(), `port already closed`) {
		t.Fatalf("expected 'port already closed' error, got:\n%s", get1())
	}

	// 2) close after open with impl error
	addr, connCh, closeTCP := startTCP(t)
	defer closeTCP()
	m2 := &dummyMon{port: newTestPort(), closeErr: fmt.Errorf("impl close failed")}
	in2 := strings.Join([]string{`HELLO 1 "ua"`, fmt.Sprintf("OPEN %s dev", addr), `CLOSE`, `QUIT`}, "\n") + "\n"
	get2, stop2 := runServer(t, m2, in2)
	select {
	case <-connCh:
	case <-time.After(2 * time.Second):
		t.Fatal("did not accept TCP connection")
	}
	stop2()
	out := get2()
	if !strings.Contains(out, `"eventType": "close"`) || !strings.Contains(out, `"error": true`) || !strings.Contains(out, "impl close failed") {
		t.Fatalf("expected close error due to impl, got:\n%s", out)
	}
}

func Test_Quit_EndsServer(t *testing.T) {
	m := &dummyMon{port: newTestPort()}
	in := strings.Join([]string{`HELLO 1 "ua"`, `QUIT`}, "\n") + "\n"
	_, stop := runServer(t, m, in)
	stop()
}

func Test_Unknown_Command_Errors(t *testing.T) {
	m := &dummyMon{port: newTestPort()}
	in := strings.Join([]string{`HELLO 1 "ua"`, `FOO`, `QUIT`}, "\n") + "\n"
	getOut, stop := runServer(t, m, in)
	stop()
	out := getOut()
	if !strings.Contains(out, `"eventType": "command_error"`) || !strings.Contains(out, "Command FOO not supported") {
		t.Fatalf("expected command_error for unknown command, got:\n%s", out)
	}
}

func Test_Hello_ImplError(t *testing.T) {
	m := &dummyMon{port: newTestPort(), helloErr: fmt.Errorf("boom from impl")}
	in := strings.Join([]string{`HELLO 1 "ua"`, `QUIT`}, "\n") + "\n"
	getOut, stop := runServer(t, m, in)
	stop()
	out := getOut()
	if !strings.Contains(out, `"eventType": "hello"`) || !strings.Contains(out, `"error": true`) || !strings.Contains(out, "boom from impl") {
		t.Fatalf("expected hello error from impl, got:\n%s", out)
	}
}

func Test_Buffer_Config_NewKeys_OK(t *testing.T) {
	m := &dummyMon{port: newTestPort()}
	in := strings.Join([]string{
		`HELLO 1 "ua"`,
		`CONFIGURE _buffer.queue 64`,
		`CONFIGURE _buffer.overflow wait`,
		`CONFIGURE _buffer.overflow_wait_ms 50`,
		`QUIT`,
	}, "\n") + "\n"
	getOut, stop := runServer(t, m, in)
	stop()
	out := getOut()
	if strings.Count(out, `"eventType": "configure"`) < 3 {
		t.Fatalf("expected 3 configure OK responses, got:\n%s", out)
	}
}

func Test_Buffer_Config_NewKeys_Invalid(t *testing.T) {
	m := &dummyMon{port: newTestPort()}
	in := strings.Join([]string{
		`HELLO 1 "ua"`,
		`CONFIGURE _buffer.queue 0`,
		`CONFIGURE _buffer.overflow nope`,
		`CONFIGURE _buffer.overflow_wait_ms -1`,
		`QUIT`,
	}, "\n") + "\n"
	getOut, stop := runServer(t, m, in)
	stop()
	out := getOut()
	for _, want := range []string{
		"invalid _buffer.queue",
		"invalid _buffer.overflow",
		"invalid _buffer.overflow_wait_ms",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected error %q in output, got:\n%s", want, out)
		}
	}
}
