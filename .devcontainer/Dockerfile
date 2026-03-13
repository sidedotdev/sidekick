FROM mcr.microsoft.com/devcontainers/go:1.24

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential cmake git ripgrep \
    && rm -rf /var/lib/apt/lists/*

# Build USearch C library from source
RUN git clone --depth 1 --recursive --branch v2.16.6 https://github.com/unum-cloud/usearch.git /tmp/usearch \
    && cd /tmp/usearch \
    && cmake -D CMAKE_BUILD_TYPE=Release \
             -D USEARCH_USE_FP16LIB=1 \
             -D USEARCH_USE_OPENMP=0 \
             -D USEARCH_USE_SIMSIMD=0 \
             -D USEARCH_USE_JEMALLOC=0 \
             -D USEARCH_BUILD_TEST_CPP=0 \
             -D USEARCH_BUILD_BENCH_CPP=0 \
             -D USEARCH_BUILD_LIB_C=1 \
             -D USEARCH_BUILD_TEST_C=0 \
             -D USEARCH_BUILD_SQLITE=0 \
             -B build_release \
    && cmake --build build_release --config Release \
    && cp build_release/libusearch_static_c.a /usr/local/lib/libusearch_c.a \
    && cp c/usearch.h /usr/local/include/usearch.h \
    && rm -rf /tmp/usearch

ENV CGO_ENABLED=1
ENV CGO_LDFLAGS="-L/usr/local/lib /usr/local/lib/libusearch_c.a -lstdc++ -lm"

# gopls for LSP tests
RUN go install golang.org/x/tools/gopls@v0.17.1

# Install bun and pre-warm the dependency cache
USER vscode
RUN curl -fsSL https://bun.sh/install | bash
ENV BUN_INSTALL=/home/vscode/.bun
ENV PATH=$BUN_INSTALL/bin:$PATH
COPY --chown=vscode:vscode frontend/package.json frontend/bun.lock /tmp/frontend-deps/
RUN cd /tmp/frontend-deps && bun install --frozen-lockfile && rm -rf /tmp/frontend-deps
USER root

# Ensure vscode user can write to Go module cache
RUN mkdir -p /go/pkg/mod && chown -R vscode:vscode /go/pkg/mod