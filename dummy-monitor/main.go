//
// This file is part of dummy-monitor.
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

package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	monitor "github.com/arduino/pluggable-monitor-protocol-handler"
	"github.com/arduino/pluggable-monitor-protocol-handler/dummy-monitor/args"
)

// dummyMonitor is an example implementation of a Monitor.
// It simulates a real implementation of a Monitor by generating
// connected ports deterministically, it can also be used for testing
// purposes.
type dummyMonitor struct {
	startSyncCount int
	closeChan      chan<- bool
}

var settings = &monitor.PortDescriptor{
	Protocol: "test",
	ConfigurationParameter: map[string]*monitor.PortParameterDescriptor{
		"speed": {
			Label:    "Baudrate",
			Type:     "enum",
			Values:   []string{"9600", "19200", "38400", "57600", "115200"},
			Selected: "9600",
		},
		"echo": {
			Label:    "echo",
			Type:     "enum",
			Values:   []string{"on", "off"},
			Selected: "on",
		},
	},
}

var openedPort *dummyPort

func main() {
	args.Parse()
	dummy := &dummyMonitor{}
	server := monitor.NewServer(dummy)
	if err := server.Run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

// Hello does nothing.
// In a real implementation it could setup background processes
// or other kind of resources necessary to discover Ports.
func (d *dummyMonitor) Hello(userAgent string, protocol int) error {
	return nil
}

func (d *dummyMonitor) Describe() (*monitor.PortDescriptor, error) {
	return settings, nil
}

func (d *dummyMonitor) Configure(parameterName string, value string) error {
	if settings.ConfigurationParameter[parameterName] == nil {
		return fmt.Errorf("could not find parameter named %s", parameterName)
	}
	values := settings.ConfigurationParameter[parameterName].Values
	for _, i := range values {
		if i == value {
			settings.ConfigurationParameter[parameterName].Selected = value
			return nil
		}
	}
	return fmt.Errorf("invalid value for parameter %s: %s", parameterName, value)
}

func (d *dummyMonitor) Open(boardPort string) (io.ReadWriter, error) {
	if openedPort != nil {
		return nil, fmt.Errorf("port already opened: %s", openedPort.portName)
	}
	openedPort = newDummyPort(boardPort)
	return openedPort, nil
}

func (d *dummyMonitor) Close() error {
	if openedPort == nil {
		return errors.New("port already closed")
	}
	openedPort.Close()
	openedPort = nil
	return nil
}

// Quit does nothing.
// In a real implementation it can be used to tear down resources
// used to monitor Ports.
func (d *dummyMonitor) Quit() {}
