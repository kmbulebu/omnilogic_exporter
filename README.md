# OmniLogic Exporter for Prometheus

This is a simple server that scrapes OmniLogic stats and exports them via HTTP for
Prometheus consumption.

## Getting Started

To run it:

```bash
./omnilogic_exporter [flags]
```

Help on flags:

```bash
./omnilogic_exporter --help
```

For more information check the [source code documentation][gdocs]. All of the
core developers are accessible via the Prometheus Developers [mailinglist][].

[gdocs]: http://godoc.org/github.com/prometheus/omnilogic_exporter
[mailinglist]: https://groups.google.com/forum/?fromgroups#!forum/prometheus-developers

## Usage

### Building

```bash
go build
```

### Testing

```bash
go test
```

### TLS and basic authentication

The OmniLogic Exporter supports TLS and basic authentication.

To use TLS and/or basic authentication, you need to pass a configuration file
using the `--web.config.file` parameter. The format of the file is described
[in the exporter-toolkit repository](https://github.com/prometheus/exporter-toolkit/blob/master/docs/web-configuration.md).

## License

Apache License 2.0, see [LICENSE](https://github.com/prometheus/omnilogic_exporter/blob/master/LICENSE).
