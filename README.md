# NanoHUB

[![CI/CD](https://github.com/micromdm/nanohub/actions/workflows/on-push-pr.yml/badge.svg)](https://github.com/micromdm/nanohub/actions/workflows/on-push-pr.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/micromdm/nanohub.svg)](https://pkg.go.dev/github.com/micromdm/nanohub)

NanoHUB adapts and unifies NanoMDM, NanoCMD, and KMFDDM into a single MDM server. Intended as a library it includes a reference command-line server as well.

## Getting started & Documentation

- [Go Reference](https://pkg.go.dev/github.com/micromdm/nanohub)  
Go package documentation for using NanoHUB as a library.

- [Operations Guide](docs/operations-guide.md)  
A brief overview of configuring and running the NanoHUB reference server.

## Getting the latest version

* Release `.zip` files containing the project should be attached to every [GitHub release](https://github.com/micromdm/nanohub/releases).
  * Release zips are also [published](https://github.com/micromdm/nanohub/actions) for every `main` branch commit.
* A Docker container is built and [published to the GHCR.io registry](http://ghcr.io/micromdm/nanohub) for every release.
  * `docker pull ghcr.io/micromdm/nanohub:latest` — `docker run ghcr.io/micromdm/nanohub:latest`
  * A Docker container is also published for every `main` branch commit (and tagged with `:main`)
* If you have a [Go toolchain installed](https://go.dev/doc/install) you can checkout the source and simply run `make`.
