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

package monitor

type message struct {
	EventType       string          `json:"eventType"`
	Message         string          `json:"message,omitempty"`
	Error           bool            `json:"error,omitempty"`
	ProtocolVersion int             `json:"protocolVersion,omitempty"`
	PortDescription *PortDescriptor `json:"port_description,omitempty"`
}

// PortDescriptor is a struct to describe the characteristic of a port
type PortDescriptor struct {
	Protocol               string                              `json:"protocol,omitempty"`
	ConfigurationParameter map[string]*PortParameterDescriptor `json:"configuration_parameters,omitempty"`
}

// PortParameterDescriptor contains characteristics for every parameter
type PortParameterDescriptor struct {
	Label    string   `json:"label,omitempty"`
	Type     string   `json:"type,omitempty"`
	Values   []string `json:"value,omitempty"`
	Selected string   `json:"selected,omitempty"`
}

func messageOk(event string) *message {
	return &message{
		EventType: event,
		Message:   "OK",
	}
}

func messageError(event, msg string) *message {
	return &message{
		EventType: event,
		Error:     true,
		Message:   msg,
	}
}
