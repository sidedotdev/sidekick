#!/bin/bash
set -e

export PATH=$PATH:/usr/local/go/bin

# Install Go if not present
if ! command -v go &> /dev/null; then
    echo "Installing Go..."
    GO_VERSION="1.23.0"
    ARCH=$(uname -m)
    if [ "$ARCH" = "aarch64" ]; then
        GO_ARCH="arm64"
    else
        GO_ARCH="amd64"
    fi
    
    wget -q "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" -O /tmp/go.tar.gz
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf /tmp/go.tar.gz
    rm /tmp/go.tar.gz
    sudo ln -s /usr/local/go/bin/go /usr/local/bin/go
fi

# Add to PATH if not already there
if ! grep -q '/usr/local/bin' ~/.bashrc; then
    echo 'export PATH=$PATH:/usr/local/bin' >> ~/.bashrc
fi

echo "Go version: $(go version)"
