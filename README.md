# Native macOS container runtime

This project explores building and running `darwin/arm64` OCI images directly
on an Apple Silicon Mac, without a Linux VM. The longer-term goal is to run
trusted applications that can use macOS frameworks and Metal while keeping a
familiar Dockerfile and Docker CLI workflow.

The walkthrough below starts with a small Go HTTP server. A patched macOS
BuildKit worker builds it from a Dockerfile through `docker buildx build` and
exports an OCI image. A local runtime starts that image through
`docker --context macnative run`. The same path has now built and run the
[Metal-backed `go-inf-server`](examples/go-inf-server/README.md) from source.
This is still an early prototype with a small set of Docker commands and
limited filesystem isolation.

## Try the tiny web server

You need an Apple Silicon Mac, Go 1.24 or later, Git, and a Docker CLI with
Buildx. Run the following commands from the root of this repository. The
BuildKit source and build outputs live under the ignored `.build/` directory.

### 1. Build and start the macOS BuildKit worker

Set up the pinned BuildKit v0.24.0 patch once:

```sh
mkdir -p .build
git clone --branch v0.24.0 --depth 1 https://github.com/moby/buildkit.git .build/buildkit-src
git -C .build/buildkit-src apply ../../patches/buildkit-v0.24.0-darwin-prototype.patch
cd .build/buildkit-src
GOCACHE="$PWD/../go-cache" go build -mod=vendor -o ../buildkitd-macos ./cmd/buildkitd
cd ../..
```

Start BuildKit in a terminal and leave it running:

```sh
.build/buildkitd-macos \
  --root .build/buildkit-state \
  --addr unix:///private/tmp/macos-buildkit.sock \
  --otel-socket-path /private/tmp/macos-buildkit-trace.sock \
  --containerd-worker=false
```

The [BuildKit prototype guide](docs/BUILDKIT_PROTOTYPE.md) explains the patch
and its current Dockerfile limits.

### 2. Build a Darwin image with Buildx

In another terminal, register the remote builder once, then build the sample
image. The named `go-toolchain` context supplies a macOS arm64 Go distribution
to the build stage so its `RUN` instruction can compile the server.

```sh
docker buildx create --name macnative --driver remote unix:///private/tmp/macos-buildkit.sock
docker buildx build --builder macnative --platform darwin/arm64 \
  --build-context "go-toolchain=$(go env GOROOT)" \
  --progress plain \
  --output type=oci,dest=.build/tiny-web.tar \
  examples/tiny-web
```

The result is an OCI tarball at `.build/tiny-web.tar`. The build does not load
the image into Docker Desktop. See the
[example README](examples/tiny-web/README.md) for details about its Dockerfile
and staged Go compiler.

### 3. Start the local Docker API service

Build the image runner and the small Docker API service:

```sh
go build -o .build/imgrun ./cmd/imgrun
go build -o .build/macd ./cmd/macd
```

Start the service in another terminal and leave it running:

```sh
.build/macd -image .build/tiny-web.tar -tag tiny-web:latest \
  -runner .build/imgrun -socket /private/tmp/macnative-docker.sock
```

Register its Docker context once:

```sh
docker context create macnative \
  --docker host=unix:///private/tmp/macnative-docker.sock
```

The Buildx builder and Docker context happen to share the name `macnative`,
but point to different sockets. The builder handles `docker buildx build`;
the context handles `docker --context macnative run` and other runtime
commands. After rebuilding the image, restart `macd` to register the new
tarball.

### 4. Run it and inspect requests

```sh
docker --context macnative run -d --name tiny-web tiny-web:latest
curl http://127.0.0.1:8080/healthz
docker --context macnative ps
docker --context macnative logs tiny-web
```

The health request returns `ok`, and the server logs its method, URI, and
client address. To watch later requests arrive, run this in another terminal:

```sh
docker --context macnative logs -f tiny-web
```

Then try another request, inspect the registered image, and clean up:

```sh
curl 'http://127.0.0.1:8080/healthz?probe=follow'
docker --context macnative images
docker --context macnative image inspect tiny-web:latest
docker --context macnative rm -f tiny-web
docker --context macnative ps -a
```

The server binds directly to host port 8080; this prototype does not support
`-p`. Plain `docker ps` uses your default Docker context, which may be Docker
Desktop. Use `--context macnative` to see containers managed by this runtime.
The [runtime guide](docs/DOCKER_RUNTIME.md) covers foreground runs, stopping a
container, and the supported Docker API calls.

## Build the Metal-backed server

The [`go-inf-server` example](examples/go-inf-server/README.md) uses a separate
source checkout as a named build context. Its Dockerfile stages Go 1.26,
compiles the cgo server with llama.cpp headers and dylibs, and exports an image
with the SmolLM2 model. The guide has the complete Buildx command and Docker
CLI run steps. On this Mac, the image loaded all 33 model layers onto the M2
Pro GPU and answered a chat request.

## Run a Pod from local Kind

The [Kubernetes prototype guide](docs/KUBERNETES.md) builds a separate tiny
server image on port 8081, registers a virtual `macnative` node with Kind,
and applies a one-replica Deployment. Kind's Linux control plane schedules
the Pod, while the application runs as a native macOS process through
`macd` and `imgrun`. This adapter requires Go 1.26 or later and currently
handles one Pod with one container; Kubernetes Service routing is a later
step.

The [local voice cluster guide](docs/VOICE_CLUSTER.md) deploys LiveKit to a
dedicated Kind cluster with UDP media forwarding. It schedules the Metal LLM,
Whisper, Kokoro, and the voice agent as native macOS Pods, and the web UI as a
Linux Pod. The [voice image guide](docs/VOICE_IMAGES.md) has the Buildx
commands for those images.

## What works and what is still experimental

The patched BuildKit worker handles the sample's `COPY` and CPU `RUN` steps
and the cgo compile for `go-inf-server`, but its shell still resolves absolute
paths on the host instead of inside the image. The runtime registers one OCI
tarball at startup and implements only
the Docker API operations needed for the example. It keeps container records
in memory and does not support `docker load`, image pull, volumes, port
mapping, or general Dockerfile and Docker Engine behavior.

The worker and runner use macOS Seatbelt to limit ordinary file writes.
Host reads and network access remain available, so run only trusted build
steps and images. A proper image-root view remains the next architectural
gate. See the [build plan](docs/BUILDPLAN.md) and
[architecture](docs/ARCHITECTURE.md) for progress and design decisions.

## Other documentation

The [image packager and local registry](docs/IMAGE_PACKAGER.md) page describes
the separate `go-inf-server` prototype, which packages an already-built Metal
application as an OCI layout. The [OCI runner](docs/RUNNER.md) can also launch
the tiny web image without the Docker CLI service.
