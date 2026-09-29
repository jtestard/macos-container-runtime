# Native llama-server image build plan

Goal: build upstream llama.cpp `llama-server` as a usable `darwin/arm64`
OCI image with Metal enabled, independent of the Go inference wrapper.

## Progress

- [x] Inspect the current builder, runner, llama.cpp source, and model.
- [x] Choose a static server build with embedded Metal code and a bundled
  SmolLM2 GGUF. Port 8082 avoids the current Go service on 8080.
- [x] Build the image with the native Buildx worker.
- [x] Run the image with `imgrun` and `docker run`; verify health and an
  OpenAI compatible chat completion. The running process loaded the Apple
  AGX Metal driver; the build embedded the Metal library and requested 99
  GPU layers. Exact offloaded layer count was not measured.
- [x] Document the build and run commands and remaining runtime limits.
- [x] Publish the verified image to the existing private Docker Hub repository
  and inspect its `darwin/arm64` OCI manifest.

The runtime currently has no model mount or command override. This first
image therefore includes the model and default arguments. A reusable image
with a model chosen at `docker run` time depends on those runtime features.

## Build

Use the sibling `go-inf-server` checkout, with its pinned llama.cpp source and
SmolLM2 model. The source context must keep the nested `tools/mtmd/models`
headers. The final image contains neither CMake nor the source tree.

```sh
SOURCE="/absolute/path/to/go-inf-server"
mkdir -p .build/llama-source
rsync -a --delete --exclude .git --exclude /build/ --exclude /models/ \
  "$SOURCE/llama.cpp/" .build/llama-source/
cat > .build/llama-source/.dockerignore <<'EOF'
.git/
/build/
/models/
.github/
tools/ui/node_modules/
EOF

# Start an isolated native BuildKit worker in another terminal:
.build/buildkitd-macos --root .build/llama-buildkit-state \
  --addr unix:///private/tmp/macos-buildkit-llama.sock \
  --otel-socket-path /private/tmp/macos-buildkit-llama-trace.sock \
  --containerd-worker=false

# Register this builder once, if it is not already listed by docker buildx ls:
docker buildx create --name macnative-llama --driver remote \
  unix:///private/tmp/macos-buildkit-llama.sock

docker buildx build --builder macnative-llama --platform darwin/arm64 \
  --build-context "llama-source=$PWD/.build/llama-source" \
  --build-context "llama-model=$SOURCE/models/smollm2-360m" \
  --build-context 'cmake-toolchain=/absolute/path/to/CMake.app/Contents' \
  --progress plain --output type=oci,dest=.build/llama-server-smollm2.tar \
  examples/llama-server
```

The Dockerfile uses the Xcode compiler on this Mac. The native BuildKit worker
currently runs `RUN` in a host snapshot, so the toolchain and compiler paths
must be available there. An isolated state directory and socket keep this
builder separate from the existing `macnative` instance.

## Run

Run directly:

```sh
.build/imgrun -image .build/llama-server-smollm2.tar
```

Or register the image with a separate local Docker-compatible daemon, then
use the ordinary Docker CLI against that socket:

```sh
# Terminal 1
.build/macd -image .build/llama-server-smollm2.tar \
  -tag llama-server:smollm2 -runner .build/imgrun \
  -socket /private/tmp/macnative-llama-docker.sock

# Terminal 2
docker -H unix:///private/tmp/macnative-llama-docker.sock \
  run -d --name llama-server llama-server:smollm2
docker -H unix:///private/tmp/macnative-llama-docker.sock ps
docker -H unix:///private/tmp/macnative-llama-docker.sock logs llama-server
curl http://127.0.0.1:8082/health
curl -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","content":"Reply with just the word hello."}],"max_tokens":16,"temperature":0}' \
  http://127.0.0.1:8082/v1/chat/completions
```

The verified health response was `{"status":"ok"}`; the chat response was
`Hello!`. The native runtime shares the Mac network. Its `docker run` endpoint
currently lacks `-p`, volume mounts, command overrides, and multiple image
registration in one daemon. The image listens only on Mac loopback port 8082.

## Private Docker Hub image

The image is published as
`jtstormz/tiny-web:llama-server-smollm2-001`, OCI index digest
`sha256:150e7b1b558f7443abff12969bcf8be1c1ae491cfda833b95112b3533138549d`.
`docker buildx imagetools inspect` confirmed a `darwin/arm64` manifest. The
current Docker Hub plan allows only the existing private `tiny-web` repository,
so the tag distinguishes this standalone server from the tiny web and Go
inference images. The native `macd` still needs a local OCI tarball; see the
[main README](../README.md#run-an-image-from-docker-hub) for its `crane pull`
and `macd` workflow. Use this image tag and port 8082 in those commands.
