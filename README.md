![gribigo-status](https://github.com/openconfig/gribigo/actions/workflows/go.yml/badge.svg)
[![Go Reference](https://pkg.go.dev/badge/github.com/openconfig/gribigo.svg)](https://pkg.go.dev/github.com/openconfig/gribigo)

![gribigo-logo](doc/gribigo.png)

This repository contains a [gRIBI](https://github.com/openconfig/gribi)
client and in-memory server, written in Go, which serves as a reference
implementation for the gRIBI protocol.

In addition, the `fluent/` directory contains a human-readable API for use in
writing functional tests against a gRIBI server. This API is designed for use intest frameworks such as [ONDATRA](https://github.com/openconfig/ondatra). The
fluent-style API is a test-only library.

**Note**: This is not an official Google product.

## Contributing

We accept external pull requests to this repository, which are subject
to the Google CLA. Please see the
[CONTRIBUTING](https://github.com/openconfig/gribigo/blob/master/CONTRIBUTING.md)
document for additional details relating to contributions.

## Licensing

This project is licensed under the Apache 2.0 license.
