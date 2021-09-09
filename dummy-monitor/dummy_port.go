package main

import (
	"io"
)

type dummyPort struct {
	echo     chan string
	portName string
}

func newDummyPort(portName string) *dummyPort {
	return &dummyPort{
		echo:     make(chan string, 20),
		portName: portName,
	}
}

func (d *dummyPort) Close() error {
	close(d.echo)
	return nil
}

func (d *dummyPort) Read(p []byte) (int, error) {
	data, ok := <-d.echo
	if !ok {
		return 0, io.EOF
	}
	n := copy(p, []byte(data))
	// if p is too small data loss may happen.. ¯\_(ツ)_/¯
	return n, nil
}

func (d *dummyPort) Write(p []byte) (int, error) {
	d.echo <- string(p)
	return len(p), nil
}
