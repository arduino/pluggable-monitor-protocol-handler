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

	"github.com/arduino/go-properties-orderedmap"
	"github.com/hashicorp/go-multierror"
)

// Port is a descriptor for a board port
type Port struct {
	Address       string          `json:"address"`
	AddressLabel  string          `json:"label,omitempty"`
	Protocol      string          `json:"protocol,omitempty"`
	ProtocolLabel string          `json:"protocolLabel,omitempty"`
	Properties    *properties.Map `json:"properties,omitempty"`
}

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
	outputChan         chan *message
	userAgent          string
	reqProtocolVersion int
	initialized        bool
	clientConn         net.Conn
	closeFuncMutex     sync.Mutex
}

// NewServer creates a new monitor server backed by the
// provided pluggable monitor implementation. To start the server
// use the Run method.
func NewServer(impl Monitor) *Server {
	return &Server{
		impl:       impl,
		outputChan: make(chan *message),
	}
}

// Run starts the protocol handling loop on the given input and
// output stream, usually `os.Stdin` and `os.Stdout` are used.
// The function blocks until the `QUIT` command is received or
// the input stream is closed. In case of IO error the error is
// returned.
func (d *Server) Run(in io.Reader, out io.Writer) error {
	go d.outputProcessor(out)
	defer close(d.outputChan)
	reader := bufio.NewReader(in)
	for {
		fullCmd, err := reader.ReadString('\n')
		if err != nil {
			d.outputChan <- messageError("command_error", err.Error())
			return err
		}
		fullCmd = strings.TrimSpace(fullCmd)
		split := strings.Split(fullCmd, " ")
		cmd := strings.ToUpper(split[0])

		if !d.initialized && cmd != "HELLO" && cmd != "QUIT" {
			d.outputChan <- messageError("command_error", fmt.Sprintf("First command must be HELLO, but got '%s'", cmd))
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
			d.outputChan <- messageOk("quit")
			return nil
		default:
			d.outputChan <- messageError("command_error", fmt.Sprintf("Command %s not supported", cmd))
		}
	}
}

func (d *Server) hello(cmd string) {
	if d.initialized {
		d.outputChan <- messageError("hello", "HELLO already called")
		return
	}
	re := regexp.MustCompile(`^(\d+) "([^"]+)"$`)
	matches := re.FindStringSubmatch(cmd)
	if len(matches) != 3 {
		d.outputChan <- messageError("hello", "Invalid HELLO command")
		return
	}
	d.userAgent = matches[2]
	v, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		d.outputChan <- messageError("hello", "Invalid protocol version: "+matches[2])
		return
	}
	d.reqProtocolVersion = int(v)
	if err := d.impl.Hello(d.userAgent, 1); err != nil {
		d.outputChan <- messageError("hello", err.Error())
		return
	}
	d.outputChan <- &message{
		EventType:       "hello",
		ProtocolVersion: 1, // Protocol version 1 is the only supported for now...
		Message:         "OK",
	}
	d.initialized = true
}

func (d *Server) describe() {
	if !d.initialized {
		d.outputChan <- messageError("describe", "Monitor not initialized")
		return
	}
	portDescription, err := d.impl.Describe()
	if err != nil {
		d.outputChan <- messageError("describe", err.Error())
		return
	}
	d.outputChan <- &message{
		EventType:       "describe",
		Message:         "OK",
		PortDescription: portDescription,
	}
}

func (d *Server) configure(cmd string) {
	if !d.initialized {
		d.outputChan <- messageError("configure", "Monitor not initialized")
		return
	}
	re := regexp.MustCompile(`^([\w.-]+) (.+)$`)
	matches := re.FindStringSubmatch(cmd)
	if len(matches) != 3 {
		d.outputChan <- messageError("configure", "Invalid CONFIGURE command")
		return
	}
	parameterName := matches[1]
	value := matches[2]
	if err := d.impl.Configure(parameterName, value); err != nil {
		d.outputChan <- messageError("configure", err.Error())
		return
	}
	d.outputChan <- &message{
		EventType: "configure",
		Message:   "OK",
	}
}

func (d *Server) open(cmd string) {
	if !d.initialized {
		d.outputChan <- messageError("open", "Monitor not initialized")
		return
	}
	parameters := strings.SplitN(cmd, " ", 2)
	if len(parameters) != 2 {
		d.outputChan <- messageError("open", "Invalid OPEN command")
		return
	}
	address := parameters[0]
	portName := parameters[1]
	port, err := d.impl.Open(portName)
	if err != nil {
		d.outputChan <- messageError("open", err.Error())
		return
	}
	d.clientConn, err = net.Dial("tcp", address)
	if err != nil {
		d.impl.Close()
		d.outputChan <- messageError("open", err.Error())
		return
	}
	// io.Copy is used to bridge the Client's TCP connection to the port one and vice versa
	go func() {
		_, err := io.Copy(port, d.clientConn) // Copy is blocking, so we run it insiede a gorutine
		if err != nil {
			d.close(err.Error())
		} else {
			d.close("lost TCP/IP connection with the client!")
		}
	}()
	go func() {
		_, err := io.Copy(d.clientConn, port) // Copy is blocking, so we run it insiede a gorutine
		if err != nil {
			d.close(err.Error())
		} else {
			d.close("lost connection with the port")
		}
	}()
	d.outputChan <- &message{
		EventType: "open",
		Message:   "OK",
	}
}

func (d *Server) close(messageErr string) {
	d.closeFuncMutex.Lock()
	defer d.closeFuncMutex.Unlock()
	if d.clientConn == nil {
		// TODO
		// d.outputChan <- messageError("close", "port already closed")
		return
	}
	connErr := d.clientConn.Close()
	portErr := d.impl.Close()
	d.clientConn = nil
	if messageErr != "" {
		d.outputChan <- messageError("port_closed", messageErr)
		return
	}
	if connErr != nil || portErr != nil {
		var errs *multierror.Error
		errs = multierror.Append(errs, connErr, portErr)
		d.outputChan <- messageError("close", errs.Error())
		return
	}
	d.outputChan <- &message{
		EventType: "close",
		Message:   "OK",
	}
}

func (d *Server) outputProcessor(outWriter io.Writer) {
	// Start go routine to serialize messages printing
	go func() {
		for msg := range d.outputChan {
			data, err := json.MarshalIndent(msg, "", "  ")
			if err != nil {
				// We are certain that this will be marshalled correctly
				// so we don't handle the error
				data, _ = json.MarshalIndent(messageError("command_error", err.Error()), "", "  ")
			}
			fmt.Fprintln(outWriter, string(data))
		}
	}()
}
