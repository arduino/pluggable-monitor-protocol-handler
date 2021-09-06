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
	"os"
	"time"

	"github.com/arduino/go-properties-orderedmap"
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

// Quit does nothing.
// In a real implementation it can be used to tear down resources
// used to monitor Ports.
func (d *dummyMonitor) Quit() {}

//TODO implement
func (d *dummyMonitor) Describe() {

}

//TODO implement
func (d *dummyMonitor) Configure() {

}

//TODO implement
func (d *dummyMonitor) Open() {

}

//TODO implement
func (d *dummyMonitor) Close() {

}

//TODO remove
// Stop is used to stop the goroutine started by StartSync
// used to discover ports.
func (d *dummyMonitor) Stop() error {
	if d.closeChan != nil {
		d.closeChan <- true
		close(d.closeChan)
		d.closeChan = nil
	}
	return nil
}

//TODO remove
// StartSync starts the goroutine that generates fake Ports.
func (d *dummyMonitor) StartSync(eventCB monitor.EventCallback, errorCB monitor.ErrorCallback) error {
	d.startSyncCount++
	if d.startSyncCount%5 == 0 {
		return errors.New("could not start_sync every 5 times")
	}

	c := make(chan bool)
	d.closeChan = c

	// Run synchronous event emitter
	go func() {
		var closeChan <-chan bool = c

		// Output initial port state
		eventCB("add", createDummyPort())
		eventCB("add", createDummyPort())

		// Start sending events
		count := 0
		for count < 2 {
			count++

			select {
			case <-closeChan:
				return
			case <-time.After(2 * time.Second):
			}

			port := createDummyPort()
			eventCB("add", port)

			select {
			case <-closeChan:
				return
			case <-time.After(2 * time.Second):
			}

			eventCB("remove", &monitor.Port{
				Address:  port.Address,
				Protocol: port.Protocol,
			})
		}

		errorCB("unrecoverable error, cannot send more events")
		<-closeChan
	}()

	return nil
}

var dummyCounter = 0

// createDummyPort creates a Port with fake data
func createDummyPort() *monitor.Port {
	dummyCounter++
	return &monitor.Port{
		Address:       fmt.Sprintf("%d", dummyCounter),
		AddressLabel:  "Dummy upload port",
		Protocol:      "dummy",
		ProtocolLabel: "Dummy protocol",
		Properties: properties.NewFromHashmap(map[string]string{
			"vid": "0x2341",
			"pid": "0x0041",
			"mac": fmt.Sprintf("%d", dummyCounter*384782),
		}),
	}
}
