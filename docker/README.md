# Docker Test Environment

This directory contains Docker configuration for running Go tests in an isolated environment.

## Quick Start

### Run tests (with Redis - default)

```bash
# From the repository root
docker compose -f docker/docker-compose.test.yml run --rm test
```

### Run tests without Redis

```bash
docker compose -f docker/docker-compose.test.yml run --rm test-without-redis

# Or build the image directly
docker build -f docker/Dockerfile.tests -t sidekick-test .
docker run --rm sidekick-test
```

## What's Included

The test Docker image (Alpine-based) includes:
- Go 1.24 (golang:1.24.0-alpine base image)
- USearch library (built from source)
- ripgrep (`rg`) for search tests
- gopls v0.17.1 for LSP tests
- Git configured for test operations

## Rebuilding

To rebuild the image after changes:

```bash
docker compose -f docker/docker-compose.test.yml build --no-cache test
```