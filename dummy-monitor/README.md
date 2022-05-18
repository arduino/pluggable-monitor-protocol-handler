# Arduino pluggable monitor reference implementation

The `dummy-monitor` tool is a command line program that interacts via stdio. It accepts commands as plain ASCII strings terminated with LF `\n` and sends response as JSON.
The "communication ports" returned by the tool are fake, this monitor is intenteded only for educational and testing purposes.

## How to build

Install a recent go environment and run `go build`. The executable `dummy-monitor` will be produced in your working directory.

## Usage

After startup, the tool will just stay idle waiting for commands. The available commands are: HELLO, DESCRIBE, CONFIGURE, OPEN, CLOSE and QUIT.

#### HELLO command

The `HELLO` command is used to establish the pluggable monitor protocol between client and monitor.
The format of the command is:

`HELLO <PROTOCOL_VERSION> "<USER_AGENT>"`

for example:

`HELLO 1 "Arduino IDE"`

or:

`HELLO 1 "arduino-cli"`

in this case the protocol version requested by the client is `1` (at the moment of writing there were no other revisions of the protocol).
The response to the command is:

```json
{
  "eventType": "hello",
  "message": "OK",
  "protocolVersion": 1
}
```

`protocolVersion` is the protocol version that the monitor is going to use in the remainder of the communication.

#### DESCRIBE command

The `DESCRIBE` command returns a description of the communication port. The description will have metadata about the port configuration, and which parameters are available:

<!-- prettier-ignore -->
```json
{
  "eventType": "describe",
  "message": "OK",
  "port_description": {
    "protocol": "test",
    "configuration_parameters": {
      "echo": {
        "label": "echo",
        "type": "enum",
        "value": [
          "on",
          "off"
        ],
        "selected": "on"
      },
      "speed": {
        "label": "Baudrate",
        "type": "enum",
        "value": [
          "9600",
          "19200",
          "38400",
          "57600",
          "115200"
        ],
        "selected": "9600"
      }
    }
  }
}
```

Each parameter has a unique name (`baudrate`, `parity`, etc...), a `type` (in this case only enum but more types will be added in the future), and the `selected` value for each parameter.

The parameter name can not contain spaces, and the allowed characters in the name are alphanumerics, underscore `_`, dot `.`, and dash `-`.

The `enum` types must have a list of possible `value`.

The client/IDE may expose these configuration values to the user via a config file or a GUI, in this case the `label` field may be used for a user readable description of the parameter.

#### CONFIGURE command

The `CONFIGURE` command sets configuration parameters for the communication port. The parameters can be changed one at a time and the syntax is:

`CONFIGURE <PARAMETER_NAME> <VALUE>`

The response to the command is:

```JSON
{
  "eventType": "configure",
  "message": "OK"
}
```

or if there is an error:

```JSON
{
  "eventType": "configure",
  "message": "invalid value for parameter speed: 123456",
  "error": true
}
```

The currently selected parameters may be obtained using the `DESCRIBE` command.

#### OPEN command

The `OPEN` command opens a communication with the board, the data exchanged with the board will be transferred to the Client/IDE via TCP/IP.

The Client/IDE must first TCP-Listen to a randomly selected port and send it to the monitor tool as part of the OPEN command. The syntax of the OPEN command is:

`OPEN <CLIENT_IP_ADDRESS> <BOARD_PORT>`

For example, let's suppose that the Client/IDE wants to communicate with the serial port `/dev/ttyACM0` then the sequence of actions to perform will be the following:

1. the Client/IDE must first listen to a random TCP port (let's suppose it chose `32123`)
1. the Client/IDE sends the command `OPEN 127.0.0.1:32123 /dev/ttyACM0` to the monitor tool
1. the monitor tool opens `/dev/ttyACM0`
1. the monitor tool connects via TCP/IP to `127.0.0.1:32123` and start streaming data back and forth

The answer to the `OPEN` command is:

```JSON
{
  "eventType": "open",
  "message": "OK"
}
```

If the monitor tool cannot communicate with the board, or if the tool can not connect back to the TCP port, or if any other error condition happens:

```JSON
{
  "eventType": "open",
  "error": true,
  "message": "unknown port /dev/ttyACM23"
}
```

The board port will be opened using the parameters previously set through the `CONFIGURE` command.

Once the port is opened, it may be unexpectedly closed at any time due to hardware failure, or because the Client/IDE closes the TCP/IP connection. In this case an asynchronous `port_closed` message must be generated from the monitor tool:

```JSON
{
  "eventType": "port_closed",
  "message": "serial port disappeared!"
}
```

or

```JSON
{
  "eventType": "port_closed",
  "message": "lost TCP/IP connection with the client!",
  "error": true
}
```

#### CLOSE command

The `CLOSE` command will close the currently opened port and close the TCP/IP connection used to communicate with the Client/IDE. The answer to the command is:

```JSON
{
  "eventType": "close",
  "message": "ok"
}
```

or in case of error

```JSON
{
  "eventType": "close",
  "message": "port already closed",
  "error": true
}
```

#### QUIT command

The `QUIT` command terminates the monitor. The response to `QUIT` is:

```JSON
{
  "eventType": "quit",
  "message": "OK"
}
```

after this output the monitor exits. This command is supposed to always succeed.

#### Invalid commands

If the client sends an invalid or malformed command, the monitor should answer with:

```JSON
{
  "eventType": "command_error",
  "message": "Command XXXX not supported",
  "error": true
}
```

### Example of usage

A possible transcript of the monitor usage:

```
HELLO 1 "test"
{
  "eventType": "hello",
  "message": "OK",
  "protocolVersion": 1
}
DESCRIBE
{
  "eventType": "describe",
  "message": "OK",
  "port_description": {
    "protocol": "test",
    "configuration_parameters": {
      "echo": {
        "label": "echo",
        "type": "enum",
        "value": [
          "on",
          "off"
        ],
        "selected": "on"
      },
      "speed": {
        "label": "Baudrate",
        "type": "enum",
        "value": [
          "9600",
          "19200",
          "38400",
          "57600",
          "115200"
        ],
        "selected": "9600"
      }
    }
  }
}
CONFIGURE speed 19200
{
  "eventType": "configure",
  "message": "OK"
}
DESCRIBE
{
  "eventType": "describe",
  "message": "OK",
  "port_description": {
    "protocol": "test",
    "configuration_parameters": {
      "echo": {
        "label": "echo",
        "type": "enum",
        "value": [
          "on",
          "off"
        ],
        "selected": "on"
      },
      "speed": {
        "label": "Baudrate",
        "type": "enum",
        "value": [
          "9600",
          "19200",
          "38400",
          "57600",
          "115200"
        ],
        "selected": "19200"
      }
    }
  }
}
OPEN 127.0.0.1:5678 "test"
{
  "eventType": "open",
  "message": "OK"
}
CLOSE
{
  "eventType": "close",
  "message": "OK"
}
QUIT
{
  "eventType": "quit",
  "message": "OK"
}
$
```

On another terminal tab to test it you can run `nc -l -p 5678` before running the `OPEN 127.0.0.1:5678 "test"` command. After that you can write messages in that terminal tab and see them being echoed.

## Security

If you think you found a vulnerability or other security-related bug in this project, please read our
[security policy](https://github.com/arduino/pluggable-monitor-protocol-handler/security/policy) and report the bug to our Security Team 🛡️
Thank you!

e-mail contact: security@arduino.cc

## License

Copyright (c) 2021 ARDUINO SA (www.arduino.cc)

The software is released under the GNU General Public License, which covers the main body
of the serial-monitor code. The terms of this license can be found at:
https://www.gnu.org/licenses/gpl-3.0.en.html

See [LICENSE.txt](https://github.com/arduino/pluggable-monitor-protocol-handler/blob/master/LICENSE.txt) for details.
