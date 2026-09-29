# Native llama-server image build plan

Goal: provide an upstream llama.cpp `llama-server` image for `darwin/arm64`
with Metal, with no model bundled by default. Supply a GGUF through a read-only
volume when starting the container.

## Progress

- [x] Build a static `llama-server` binary with the Metal library embedded.
- [x] Remove the model from the final image. The OCI tarball is 7.3 MB.
- [x] Accept read-only `docker run -v` binds and command arguments in `macd`
  and `imgrun`.
- [x] Run with SmolLM2 mounted from the sibling project; verify `/health`,
  `docker ps`, `docker logs`, `docker inspect`, and a chat completion.
- [x] Check the bind parser and runner path validation with focused Go tests.
- [x] Publish and inspect the model-free image in the private Docker Hub
  repository.

The earlier `jtstormz/tiny-web:llama-server-smollm2-001` image contains a
model. It remains an explicit, versioned example rather than the default.

## Build

The sibling `go-inf-server` checkout supplies the pinned llama.cpp source.
Keep the nested `tools/mtmd/models` headers when staging it. The final image
contains only the server binary.

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

# In another terminal, run a native BuildKit worker:
.build/buildkitd-macos --root .build/llama-buildkit-state \
  --addr unix:///private/tmp/macos-buildkit-llama.sock \
  --otel-socket-path /private/tmp/macos-buildkit-llama-trace.sock \
  --containerd-worker=false

# Register once if docker buildx ls does not list macnative-llama:
docker buildx create --name macnative-llama --driver remote \
  unix:///private/tmp/macos-buildkit-llama.sock

docker buildx build --builder macnative-llama --platform darwin/arm64 \
  --build-context "llama-source=$PWD/.build/llama-source" \
  --build-context 'cmake-toolchain=/absolute/path/to/CMake.app/Contents' \
  --progress plain --output type=oci,dest=.build/llama-server.tar \
  examples/llama-server
```

The Dockerfile uses this Mac's Xcode compiler. The current native BuildKit
worker runs `RUN` in a host snapshot, so that compiler path must exist on the
Mac. The CMake executable comes from the named build context.

## Run with a model volume

Start a local daemon for the image:

```sh
.build/macd -image .build/llama-server.tar -tag llama-server:local \
  -runner .build/imgrun -socket /private/tmp/macnative-llama-docker.sock
```

In another terminal, mount a model directory and name the model to load:

```sh
SOURCE="/absolute/path/to/go-inf-server"
docker -H unix:///private/tmp/macnative-llama-docker.sock run -d \
  --name llama-server \
  -v "$SOURCE/models/smollm2-360m:/app/models:ro" \
  llama-server:local -m models/SmolLM2-360M-Instruct-Q8_0.gguf

docker -H unix:///private/tmp/macnative-llama-docker.sock ps
docker -H unix:///private/tmp/macnative-llama-docker.sock logs llama-server
curl http://127.0.0.1:8082/health
curl -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","content":"Reply with just the word hello."}],"max_tokens":16,"temperature":0}' \
  http://127.0.0.1:8082/v1/chat/completions
```

The verified responses were `{"status":"ok"}` and `Hello!`. On startup,
`/health` can return HTTP 503 while the model loads. The process opened the
GGUF on the host volume and loaded the Apple AGX Metal driver. The image
requests 99 GPU layers; the exact number offloaded was not measured.

The current runner does not remap `/` into the image. Mount the directory at
`/app/models` and pass a **relative** model path such as `models/file.gguf`.
Only read-only `-v` binds are supported. The target must be absent in the image,
and the sandbox denies writes outside its temporary state. The runtime still
does not support `-p`, entrypoint overrides, or `docker pull`; it uses the Mac's
network namespace and binds loopback port 8082.

## Distribution

The default model-free image is `jtstormz/tiny-web:llama-server-001`, OCI
index digest
`sha256:ce40c59fa3cba71fce515d6dc5be37fd17ae6c97482e74f0ddfa8c0bb577ad05`.
`docker buildx imagetools inspect` confirmed its `darwin/arm64` manifest. The
existing private `tiny-web` repository is reused because this Docker Hub plan
does not allow another private repository. The local daemon still needs an OCI
tarball; follow the [main README's download steps](../README.md#run-an-image-from-docker-hub)
with this tag, then start `macd` and use the volume-backed `docker run` command
above with the matching tag.

The prior bundled image remains available at
`jtstormz/tiny-web:llama-server-smollm2-001`, index digest
`sha256:150e7b1b558f7443abff12969bcf8be1c1ae491cfda833b95112b3533138549d`.
For registry downloads, see the [main README](../README.md#run-an-image-from-docker-hub).
