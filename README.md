# Native macOS container runtime

Run `darwin/arm64` OCI images as native processes on an Apple Silicon Mac. This
project is an early container runtime and image builder for trusted macOS
workloads, including applications that use Metal. It exposes a small Docker
Engine API so the regular Docker CLI can start and inspect images through a
`macnative` context. A patched BuildKit worker also accepts `docker buildx build`
and exports Darwin images from Dockerfiles.

The runtime does not use a Linux VM. Images contain macOS arm64 programs, not
Linux binaries. Processes share the Mac's network and have limited filesystem
isolation; only run images and Dockerfile steps you trust. See the
[architecture](docs/ARCHITECTURE.md) and [build plan](docs/BUILDPLAN.md) for the
design and current progress.

## Install the runtime

You need an Apple Silicon Mac, Go 1.24 or later, Git, and the Docker CLI. A
Docker daemon is not needed to run an existing Darwin image. Clone the
repository and install the two runtime programs:

```sh
git clone https://github.com/jtestard/macos-container-runtime.git
cd macos-container-runtime
./install.sh
./scripts/enable-macd.sh
```

`install.sh` builds and installs `macd` and `imgrun` in your user Application
Support directory, prepares an image store, and creates the `macnative` Docker
context. It does not change your current context, so Docker Desktop stays your
default. The separate startup script loads a user LaunchAgent, so `macd` starts
at login and restarts if it exits. Docker Desktop does not need to be running.

The examples below start with `docker context use macnative`, so plain
`docker` commands reach the native runtime, and end with
`docker context use desktop-linux` to return to Docker Desktop. The context
setting persists across shells until you switch back, so while `macnative` is
selected, tools such as `kind` also talk to the native runtime. To target it
for a single command instead, use `docker --context macnative ...`.

Run `./scripts/disable-macd.sh` to stop and remove the LaunchAgent. See the
[runtime guide](docs/DOCKER_RUNTIME.md) for manual startup and configuration.

## Run a simple web server

Pull the small `darwin/arm64` Go server from Docker Hub and run it with the
regular Docker CLI:

```sh
docker context use macnative
docker pull jtstormz/tiny-web:dev-002
docker run -d --name tiny-web jtstormz/tiny-web:dev-002 --port 20081
curl http://127.0.0.1:20081/healthz
docker logs tiny-web
docker rm -f tiny-web
docker context use desktop-linux
```

The server listens directly on the Mac. `--port` selects an available port;
without it, this published `dev-002` image uses 8080 (builds from the current
source default to 20081). Its source is in
[examples/tiny-web](examples/tiny-web/README.md).

## Run a llama server with Metal

This model-free llama.cpp image runs natively on macOS and uses Metal. Keep the
GGUF in a directory on the Mac and mount it read-only into the image. See the
[image composition note](docs/LLAMA_SERVER_IMAGE.md) for its build inputs and
volume contract. The commands below download the small SmolLM2 model (about
390 MB) from Hugging Face into `~/models/smollm2-360m`; to use a GGUF you
already have, set `MODEL_DIR` to its directory and change the `-m` file name.

```sh
MODEL_DIR="$HOME/models/smollm2-360m"
mkdir -p "$MODEL_DIR"
curl -fL -o "$MODEL_DIR/SmolLM2-360M-Instruct-Q8_0.gguf" \
  https://huggingface.co/bartowski/SmolLM2-360M-Instruct-GGUF/resolve/main/SmolLM2-360M-Instruct-Q8_0.gguf
docker context use macnative
docker pull jtstormz/llama-server:ee0445c-2
docker run -d --name llama-server \
  -v "$MODEL_DIR:/app/models:ro" \
  jtstormz/llama-server:ee0445c-2 \
  -m models/SmolLM2-360M-Instruct-Q8_0.gguf
docker logs llama-server
curl http://127.0.0.1:20082/health
docker rm -f llama-server
docker context use desktop-linux
```

The server may return HTTP 503 from `/health` while loading the model. It
listens on the Mac's port 20082, which must be free.

Both examples use the Mac's network directly; `-p` is not supported. Pulled
images remain available after `macd` restarts. Native containers appear only
while the `macnative` context is selected, not in Docker Desktop's `docker ps`.

The [runtime guide](docs/DOCKER_RUNTIME.md) lists the supported Docker
commands and current limitations.

## Build a Darwin image

The native builder is a patched BuildKit v0.24.0 worker. Set it up once:

```sh
git clone --branch v0.24.0 --depth 1 https://github.com/moby/buildkit.git .build/buildkit-src
git -C .build/buildkit-src apply ../../patches/buildkit-v0.24.0-darwin-prototype.patch
cd .build/buildkit-src
GOCACHE="$PWD/../go-cache" go build -mod=vendor -o ../buildkitd-macos ./cmd/buildkitd
cd ../..
```

Start BuildKit in a terminal:

```sh
.build/buildkitd-macos \
  --root .build/buildkit-state \
  --addr unix:///private/tmp/macos-buildkit.sock \
  --otel-socket-path /private/tmp/macos-buildkit-trace.sock \
  --containerd-worker=false
```

Register it once as a remote Buildx builder named `macnative-build`:

```sh
docker buildx create --name macnative-build --driver remote unix:///private/tmp/macos-buildkit.sock
```

The two names are deliberate. `macnative` is the **Docker context** that
`install.sh` created; it points at `macd`'s socket and runs images.
`macnative-build` is the **Buildx builder**; it points at the BuildKit socket
and builds images. They cannot share a name because Buildx treats every
Docker context as a builder of the same name, so
`docker buildx create --name macnative` fails with `instance name "macnative"
already exists as context builder`. Pass `--builder macnative-build` to
`docker buildx build`; `docker buildx ls` lists both.

Buildx exports an OCI tarball for the runtime. The
[BuildKit guide](docs/BUILDKIT_PROTOTYPE.md) documents the patch and
Dockerfile limits. The build does not load the image into Docker Desktop.

## Kubernetes

Two scripts create a local Kind cluster with this Mac as a native node, and
remove both again:

```sh
./scripts/kube-up.sh
./scripts/kube-down.sh
```

The [Kubernetes usage guide](docs/KUBERNETES.md) explains them and runs a
llama server on the Mac node. To talk to a voice assistant whose models all
run natively on the Mac, follow the
[voice assistant quickstart](docs/VOICE_QUICKSTART.md); the
[voice cluster guide](docs/VOICE_CLUSTER.md) explains how it fits together.

## Current limits

`macd` keeps container records in memory and pulled images on disk.
The Docker API supports a narrow set of commands including `run`, `ps`,
`logs`, `stop`, and `rm`. Read-only `-v` binds and command arguments are
supported; see the [llama-server image](docs/LLAMA_SERVER_IMAGE.md).
`docker pull` accepts `darwin/arm64` images. There is no `docker load` or port mapping yet. The
builder's shell still resolves absolute paths on the host rather than inside
the image. The worker and runner use
macOS Seatbelt to limit ordinary file writes, while host reads and network
access remain available.

The [OCI runner guide](docs/RUNNER.md) explains how to run a local tarball
without the Docker API service.

## Examples

The [tiny-web example](examples/tiny-web/README.md) is a small Go HTTP server.
With the native BuildKit worker running, build it from its Dockerfile:

```sh
docker buildx build --builder macnative-build --platform darwin/arm64 \
  --build-context "go-toolchain=$(go env GOROOT)" \
  --progress plain \
  --output type=oci,dest=.build/tiny-web.tar \
  examples/tiny-web
```

For this local tarball, stop the LaunchAgent and start `macd` manually on the
usual socket:

```sh
./scripts/disable-macd.sh
.build/macd -image .build/tiny-web.tar -tag tiny-web:latest \
  -runner .build/imgrun -socket /private/tmp/macnative-docker.sock
```

In another terminal, choose an available port:

```sh
docker context use macnative
docker run -d --name tiny-web tiny-web:latest --port 20081
curl http://127.0.0.1:20081/healthz
docker rm -f tiny-web
docker context use desktop-linux
```

The server defaults to port 20081 when `--port` is omitted. The
[macos-native-images](https://github.com/jtestard/macos-native-images)
repository builds the llama.cpp, whisper.cpp and Kokoro images from upstream
source with this builder.
