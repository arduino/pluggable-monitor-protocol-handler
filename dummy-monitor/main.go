// This file is part of dummy-monitor.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

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
