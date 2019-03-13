# go-avahi-register [![CircleCI](https://circleci.com/gh/josegonzalez/go-avahi-register.svg?style=svg)](https://circleci.com/gh/josegonzalez/go-avahi-register)

A tool for registering services against avahi/bonjour.

## Installation

Install it using the "go get" command:

    go get github.com/josegonzalez/go-avahi-register

## Usage

Create a `config.json` file. This file will contain all services that will be registered to the current IP address. An example is as follows:

```json
{
  "services": [
    {
      "name": "nodejs-express",
      "port": 80,
      "scheme": "http"
    },
    {
      "name": "personal-apt-repository",
      "port": 80,
      "scheme": "apt"
    },
    {
      "name": "awesome-ntp-server",
      "port": 123,
      "scheme": "ntp",
      "protocol": "udp"
    }
  ]
}
```

Using the above `config.json`, `avahi-register` may be triggered as follows:

```shell
avahi-register -config config.json
```

The `avahi-register` process responds to signals, and will reload the `config.json` on `SIGHUP`. Note that an invalid `config.json` will result in a hard crash of `avahi-register`.

### Service Schema

The schema for a service is as follows:

- `name` (required, type: string): the name of the service to register
- `port` (optional, type: int, default: `80`): the port on which the service is listening
- `scheme` (optional, type: string, default: `http`): A scheme that will be used to register an avahi service-type.
- `protocol` (optional, type: string, default: `tcp`): A protocol that will be used to register an avahi service-type. If the `scheme` is `http` or `http`, the `protocol` **must** be `tcp`.
