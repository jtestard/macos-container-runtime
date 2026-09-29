# Build `go-inf-server` with native Buildx

This example compiles the real `go-inf-server` cgo application from source in a
`darwin/arm64` BuildKit `RUN` step. The final OCI image contains the server,
its llama.cpp dylibs, `config.smollm2.toml`, and one SmolLM2 GGUF model. The
Go toolchain, headers, sources, and module cache stay in the build stage.

The source checkout remains separate from this repository. It must already
contain the llama.cpp headers and dylibs produced by `make llama`, plus
`models/smollm2-360m/SmolLM2-360M-Instruct-Q8_0.gguf`. The Dockerfile does
not rebuild llama.cpp or download the model.

## Build

Start the patched native BuildKit worker and register the `macnative` Buildx
builder as described in the [root README](../../README.md). The source module
requires Go 1.26. On this Mac, the downloaded Go toolchain's module-cache
directories were read-only, so the local context uses a writable copy. Run
these commands from this repository's root:

```sh
APP_SOURCE='/absolute/path/to/go-inf-server'
mkdir -p .build
GO_TOOLCHAIN="$(GOMODCACHE="$PWD/.build/toolchain-modcache" GOTOOLCHAIN=auto \
  go -C "$APP_SOURCE" env GOROOT)"
cp -R "$GO_TOOLCHAIN" .build/go1.26-writable
chmod -R u+w .build/go1.26-writable

docker buildx build --builder macnative --platform darwin/arm64 \
  --build-context "go-toolchain=$PWD/.build/go1.26-writable" \
  --build-context "app=$APP_SOURCE" \
  --progress plain \
  --output type=oci,dest=.build/go-inf-server-smollm2.tar \
  examples/go-inf-server
```

The `app` context is the source checkout. Each `COPY` selects only the files
needed by the build or final image. `RUN` invokes the staged Go 1.26 compiler
with cgo, Xcode's clang, and the macOS SDK. It downloads the Go module needed
for the server through the Mac's host network. The current worker still runs
the host shell and resolves absolute paths on the host, so the Dockerfile
derives the snapshot path to reach the staged toolchain. It needs a local
Xcode installation at `/Applications/Xcode.app`.

Inspect the exported image:

```sh
go build -o .build/imgrun ./cmd/imgrun
.build/imgrun -image .build/go-inf-server-smollm2.tar -inspect
```

On this Mac, the OCI tar was 361 MB. Its config declares `darwin/arm64`,
`/app` as the working directory, and `./bin/server -config
config.smollm2.toml` as the entrypoint. The extracted entrypoint is an arm64
Mach-O binary. The final image contains no Go toolchain.

## Run through Docker CLI

Build the local Docker API service and start it in another terminal. The
service registers one OCI tarball at startup; restart it if it is already
serving the tiny-web image or after rebuilding this tarball.

```sh
go build -o .build/macd ./cmd/macd
.build/macd -image .build/go-inf-server-smollm2.tar \
  -tag go-inf-server:smollm2 -runner .build/imgrun \
  -socket /private/tmp/macnative-docker.sock
```

The `macnative` Docker context from the [root README](../../README.md) points
to this socket. In a separate terminal:

```sh
docker --context macnative run -d --name go-inf-server go-inf-server:smollm2
docker --context macnative ps
docker --context macnative logs go-inf-server
curl http://127.0.0.1:8080/healthz
curl -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","content":"Reply with exactly: pong"}],"max_completion_tokens":16,"temperature":0}' \
  http://127.0.0.1:8080/v1/chat/completions
```

Startup takes time while the GGUF and Metal kernels load. The tested health
response was `{}` with HTTP 200; the chat request returned `pong`. Docker
logs reported an Apple M2 Pro device and `offloaded 33/33 layers to GPU`.
The server listens directly on host port 8080, so `-p` is not supported or
needed. Builds and runtime processes share the Mac's host network.

To stop and remove the container:

```sh
docker --context macnative rm -f go-inf-server
```

The [runtime guide](../../docs/DOCKER_RUNTIME.md) describes the supported
Docker commands and current isolation limits.
