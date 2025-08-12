//
// This file is part of pluggable-monitor-protocol-handler.
//
// Copyright 2021 ARDUINO SA (http://www.arduino.cc/)
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

// Package monitor is a library for handling the Arduino Pluggable-Monitor protocol
// (https://github.com/arduino/tooling-rfcs/blob/main/RFCs/0004-pluggable-monitor.md#pluggable-monitor-api-via-stdinstdout)
//
// The library implements the state machine and the parsing logic to communicate with a pluggable-monitor client.
// All the commands issued by the client are conveniently translated into function calls, in particular
// the Monitor interface are the only functions that must be implemented to get a fully working pluggable monitor
// using this library.
//
// A usage example is provided in the dummy-monitor package.
package monitor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"
)

// Monitor is an interface that represents the business logic that
// a pluggable monitor must implement. The communication protocol
// is completely hidden and it's handled by a MonitorServer.
type Monitor interface {
	// Hello is called once at startup to provide the userAgent string
	// and the protocolVersion negotiated with the client.
	Hello(userAgent string, protocolVersion int) error

	// Describe is called to obtain the description of the communication port
	Describe() (*PortDescriptor, error)

	// Configure allows to set the configuration parameters for the communication port
	Configure(parameterName string, value string) error

	// Open allows to open a communication with the board using TCP/IP
	Open(boardPort string) (io.ReadWriter, error)

	// Close will close the currently open port and TCP/IP connection
	Close() error

	// Quit is called just before the server terminates. This function can be
	// used by the monitor as a last chance to gracefully close resources.
	Quit()
}

// A Server is a pluggable monitor protocol handler,
// it must be created using the NewServer function.
type Server struct {
	impl               Monitor
	out                io.Writer
	outMutex           sync.Mutex
	userAgent          string
	reqProtocolVersion int
	initialized        bool
	clientConn         net.Conn
	closeFuncMutex     sync.Mutex
	bufCfg             BufferConfig
}

// NewServer creates a new monitor server backed by the
// provided pluggable monitor implementation. To start the server
// use the Run method.
func NewServer(impl Monitor, opts ...Option) *Server {
	s := &Server{
		impl:   impl,
		bufCfg: BufferConfig{HighWaterMark: 1}, // default == unbuffered
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run starts the protocol handling loop on the given input and
// output stream, usually `os.Stdin` and `os.Stdout` are used.
// The function blocks until the `QUIT` command is received or
// the input stream is closed. In case of IO error the error is
// returned.
func (d *Server) Run(in io.Reader, out io.Writer) error {
	d.out = out
	reader := bufio.NewReader(in)
	for {
		fullCmd, err := reader.ReadString('\n')
		if err != nil {
			d.outputMessage(messageError("command_error", err.Error()))
			return err
		}
		fullCmd = strings.TrimSpace(fullCmd)
		split := strings.Split(fullCmd, " ")
		cmd := strings.ToUpper(split[0])

		if !d.initialized && cmd != "HELLO" && cmd != "QUIT" {
			d.outputMessage(messageError("command_error", fmt.Sprintf("First command must be HELLO, but got '%s'", cmd)))
			continue
		}

		switch cmd {
		case "HELLO":
			d.hello(fullCmd[6:])
		case "DESCRIBE":
			d.describe()
		case "CONFIGURE":
			d.configure(fullCmd[10:])
		case "OPEN":
			d.open(fullCmd[5:])
		case "CLOSE":
			d.close("")
		case "QUIT":
			d.impl.Quit()
			d.outputMessage(messageOk("quit"))
			return nil
		default:
			d.outputMessage(messageError("command_error", fmt.Sprintf("Command %s not supported", cmd)))
		}
	}
}

func (d *Server) hello(cmd string) {
	if d.initialized {
		d.outputMessage(messageError("hello", "HELLO already called"))
		return
	}
	re := regexp.MustCompile(`^(\d+) "([^"]+)"$`)
	matches := re.FindStringSubmatch(cmd)
	if len(matches) != 3 {
		d.outputMessage(messageError("hello", "Invalid HELLO command"))
		return
	}
	d.userAgent = matches[2]
	v, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		d.outputMessage(messageError("hello", "Invalid protocol version: "+matches[2]))
		return
	}
	d.reqProtocolVersion = int(v)
	if err := d.impl.Hello(d.userAgent, 1); err != nil {
		d.outputMessage(messageError("hello", err.Error()))
		return
	}
	d.outputMessage(&message{
		EventType:       "hello",
		ProtocolVersion: 1, // Protocol version 1 is the only supported for now...
		Message:         "OK",
	})
	d.initialized = true
}

func (d *Server) describe() {
	if !d.initialized {
		d.outputMessage(messageError("describe", "Monitor not initialized"))
		return
	}
	portDescription, err := d.impl.Describe()
	if err != nil {
		d.outputMessage(messageError("describe", err.Error()))
		return
	}
	d.outputMessage(&message{
		EventType:       "describe",
		Message:         "OK",
		PortDescription: portDescription,
	})
}

func (d *Server) configure(cmd string) {
	if !d.initialized {
		d.outputMessage(messageError("configure", "Monitor not initialized"))
		return
	}
	re := regexp.MustCompile(`^([\w.-]+) (.+)$`)
	matches := re.FindStringSubmatch(cmd)
	if len(matches) != 3 {
		d.outputMessage(messageError("configure", "Invalid CONFIGURE command"))
		return
	}
	parameterName := matches[1]
	value := matches[2]

	switch parameterName {
	case "_buffer.hwm":
		if v, err := strconv.Atoi(value); err == nil && v >= 1 {
			d.bufCfg.HighWaterMark = v
			d.outputMessage(messageOk("configure"))
			return
		}
		d.outputMessage(messageError("configure", "invalid _buffer.hwm"))
		return
	case "_buffer.interval_ms":
		if v, err := strconv.Atoi(value); err == nil && v >= 0 {
			d.bufCfg.FlushInterval = time.Duration(v) * time.Millisecond
			d.outputMessage(messageOk("configure"))
			return
		}
		d.outputMessage(messageError("configure", "invalid _buffer.interval_ms"))
		return
	case "_buffer.line":
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on":
			d.bufCfg.LineBuffering = true
		case "false", "0", "no", "off":
			d.bufCfg.LineBuffering = false
		default:
			d.outputMessage(messageError("configure", "invalid _buffer.line"))
			return
		}
		d.outputMessage(messageOk("configure"))
		return
	case "_buffer.queue":
		if v, err := strconv.Atoi(value); err == nil && v >= 1 {
			d.bufCfg.FlushQueueCapacity = v
			d.outputMessage(messageOk("configure"))
			return
		}
		d.outputMessage(messageError("configure", "invalid _buffer.queue"))
		return
	case "_buffer.overflow":
		switch strings.ToLower(value) {
		case "drop", "wait":
			d.bufCfg.OverflowStrategy = strings.ToLower(value)
			d.outputMessage(messageOk("configure"))
			return
		default:
			d.outputMessage(messageError("configure", "invalid _buffer.overflow"))
			return
		}
	case "_buffer.overflow_wait_ms":
		if v, err := strconv.Atoi(value); err == nil && v >= 0 {
			d.bufCfg.OverflowWait = time.Duration(v) * time.Millisecond
			d.outputMessage(messageOk("configure"))
			return
		}
		d.outputMessage(messageError("configure", "invalid _buffer.overflow_wait_ms"))
		return
	}

	if err := d.impl.Configure(parameterName, value); err != nil {
		d.outputMessage(messageError("configure", err.Error()))
		return
	}
	d.outputMessage(&message{
		EventType: "configure",
		Message:   "OK",
	})
}

func (d *Server) open(cmd string) {
	if !d.initialized {
		d.outputMessage(messageError("open", "Monitor not initialized"))
		return
	}
	parameters := strings.SplitN(cmd, " ", 2)
	if len(parameters) != 2 {
		d.outputMessage(messageError("open", "Invalid OPEN command"))
		return
	}
	address := parameters[0]
	portName := parameters[1]
	port, err := d.impl.Open(portName)
	if err != nil {
		d.outputMessage(messageError("open", err.Error()))
		return
	}
	d.clientConn, err = net.Dial("tcp", address)
	if err != nil {
		d.impl.Close()
		d.outputMessage(messageError("open", err.Error()))
		return
	}
	conn := d.clientConn
	// Port -> TCP (with optional buffering)
	go func() {
		defer d.close("port_disconnected")
		agg := newAggregator(d.bufCfg)
		defer agg.close()

		// read from port in small chunks and feed aggregator
		go func() {
			defer agg.close()
			buf := make([]byte, 256)
			for {
				n, err := port.Read(buf)
				if n > 0 {
					agg.addChunk(buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()

		// drain aggregated payloads to TCP
		_ = agg.drainTo(conn)
	}()
	// TCP -> Port (no buffering; low latency for user input)
	go func() {
		defer d.close("client_disconnected")
		// Capture the current clientConn into a local variable to avoid
		// racy reads of d.clientConn while close() may set it to nil.
		buf := make([]byte, 256)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if _, werr := port.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	d.outputMessage(&message{
		EventType: "open",
		Message:   "OK",
	})
}

func (d *Server) close(messageErr string) {
	d.closeFuncMutex.Lock()
	defer d.closeFuncMutex.Unlock()
	if d.clientConn == nil {
		if messageErr == "" {
			d.outputMessage(messageError("close", "port already closed"))
		}
		return
	}
	connErr := d.clientConn.Close()
	portErr := d.impl.Close()
	d.clientConn = nil
	if messageErr != "" {
		d.outputMessage(messageError("port_closed", messageErr))
		return
	}
	if connErr != nil || portErr != nil {
		var errs *multierror.Error
		errs = multierror.Append(errs, connErr, portErr)
		d.outputMessage(messageError("close", errs.Error()))
		return
	}
	d.outputMessage(&message{
		EventType: "close",
		Message:   "OK",
	})
}

func (d *Server) outputMessage(msg *message) {
	data, err := json.MarshalIndent(msg, "", "  ")
	if err != nil {
		// We are certain that this will be marshalled correctly so we don't handle the error
		data, _ = json.MarshalIndent(messageError("command_error", err.Error()), "", "  ")
	}
	d.outMutex.Lock()
	fmt.Fprintln(d.out, string(data))
	d.outMutex.Unlock()
}
