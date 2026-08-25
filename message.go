// This file is part of pluggable-monitor-protocol-handler.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

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
