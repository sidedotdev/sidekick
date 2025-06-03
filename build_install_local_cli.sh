#!/bin/sh
set -ex

cd frontend
if [ ! -d "node_modules" ]; then
  npm ci
else
  npm i
fi
npm run build
cd ..

export VERSION=$(git rev-parse --short HEAD)
export CGO_ENABLED=1
export CGO_LDFLAGS="-L. libusearch_c.a -lstdc++ -lm"
go build -ldflags="-X main.version=${VERSION}" -o side sidekick/cli
sudo mv side /usr/local/bin/side
