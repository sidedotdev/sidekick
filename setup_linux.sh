#!/bin/bash
set -e

export PATH=$PATH:/usr/local/go/bin

# Install build dependencies
if ! command -v g++ &> /dev/null; then
    echo "Installing build-essential (g++, make, etc.)..."
    sudo apt-get update
    sudo apt-get install -y build-essential
fi

# Install ripgrep (needed for search tests)
if ! command -v rg &> /dev/null; then
    echo "Installing ripgrep..."
    sudo apt-get update
    sudo apt-get install -y ripgrep
fi

# Install C++ standard library headers (needed for usearch)
if [ ! -f /usr/include/c++/11/algorithm ]; then
    echo "Installing libstdc++ headers..."
    sudo apt-get update
    sudo apt-get install -y libstdc++-11-dev
fi

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

# Install usearch C library for vector search
USEARCH_LIB="/usr/local/lib/libusearch_c.so"
if [ ! -f "$USEARCH_LIB" ]; then
    echo "Installing usearch C library..."
    sudo apt-get update
    sudo apt-get install -y cmake
    USEARCH_TMP="/tmp/usearch"
    rm -rf "$USEARCH_TMP"
    git clone --depth 1 --recurse-submodules https://github.com/unum-cloud/usearch.git "$USEARCH_TMP"
    cd "$USEARCH_TMP"
    cmake -B build_release \
        -DUSEARCH_BUILD_LIB_C=1 \
        -DUSEARCH_BUILD_TEST_CPP=0 \
        -DUSEARCH_BUILD_BENCH_CPP=0 \
        -DCMAKE_BUILD_TYPE=Release
    cmake --build build_release --config Release
    sudo cp build_release/libusearch_c.so /usr/local/lib/
    sudo cp c/usearch.h /usr/local/include/
    sudo ldconfig
    cd -
    rm -rf "$USEARCH_TMP"
fi

echo "Go version: $(go version)"
