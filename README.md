# go-avahi-register ![build status](https://github.com/josegonzalez/go-avahi-register/actions/workflows/build.yml/badge.svg)

A tool for registering services against avahi/bonjour.

## Requirements

- A Linux distribution: This project may work on other systems, but has only been tested on Linux.
- The `avahi-daemon` package: This may be installed on Debian-based systems via `apt-get`

## Installation

Install it using the "go get" command:

    go get github.com/josegonzalez/go-avahi-register

## Configuration

`go-avahi-register` supports a single configuration file can be specified via
the `--config` flag. By default, this value is set to
`/etc/avahi-register/config.$FORMAT`, where `$FORMAT` is one of the following
(detected in order):

- json
- yml
- yaml

Create a `config.yml` file. This file will contain all services that will be registered to the current IP address. An example is as follows:

```yaml
---
services:
- name: nodejs-express
  port: 80
  scheme: http
- name: personal-apt-repository
  port: 80
  scheme: apt
- name: awesome-ntp-server
  port: 123
  scheme: ntp
  protocol: udp
```

### Service Schema

The schema for a service is as follows:

- `name`: _required_
  - type: `string`
  - description: The name of the service to register.
- `port`: _optional_
  - type: `int`
  - default: `80`
  - description: The port on which the service is listening.
- `scheme`: _optional_
  - type: `string`
  - default: `http`
  - description: A scheme that will be used to register an avahi service-type.
- `protocol`: _optional_
  - type: `string`
  - default: `tcp`
  - description: A protocol that will be used to register an avahi service-type. If the `scheme` is `http` or `http`, the `protocol` **must** be `tcp`.

## Usage

### Running avahi-register

Using the above config file, `avahi-register` may be triggered as follows:

```shell
avahi-register run
```

By default, `avahi-register` will register against the first network interface with an IPv4 IP Address that is not a loopback device. In the case where this may be ambiguous or result in the wrong IP Address being selected, the value may be overriden with the `-ip-address` flag:

```shell
avahi-register run --ip-address 192.168.1.2
```

The `avahi-register` process responds to signals, and will reload the config file on `SIGHUP` or when the file is changed. Note that an invalid config file will result in a hard crash of `avahi-register`.

### Adding a new entry

The `add` command can be used to add a new entry:

```shell
avahi-register add --name irc
```

See the `avahi-register add --help` output for more information.

### Cat the config

The `cat` command can be used to display the config file:

```shell
avahi-register cat
```

See the `avahi-register cat --help` output for more information.

### Initialize a config

The `init` command can be used to initialize a config file:

```shell
avahi-register init
```

See the `avahi-register init --help` output for more information.

### Removing an entry

The `remove` command can be used to remove an entry:

```shell
avahi-register remove --name irc
```

See the `avahi-register remove --help` output for more information.

### Showing the config

The `show-config` command can be used to display a human readable version of the config:

```shell
avahi-register show-config
```

See the `avahi-register show-config --help` output for more information.
