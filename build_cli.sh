#!/bin/sh

export CGO_ENABLED=1
export CGO_LDFLAGS="-L. libusearch_c.a -lstdc++ -lm"
go build -o side sidekick/cli
