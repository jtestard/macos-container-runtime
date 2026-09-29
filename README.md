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
context. The separate startup script loads a user LaunchAgent, so `macd` starts
at login and restarts if it exits. Docker Desktop does not need to be running.
Run `./scripts/disable-macd.sh` to stop and remove the LaunchAgent. See the
[runtime guide](docs/DOCKER_RUNTIME.md) for manual startup and configuration.

## Run a simple web server

Pull the small `darwin/arm64` Go server from Docker Hub and run it with the
regular Docker CLI:

```sh
docker --context macnative pull jtstormz/tiny-web:dev-001
docker --context macnative run -d --name tiny-web jtstormz/tiny-web:dev-001
curl http://127.0.0.1:8080/healthz
docker --context macnative logs tiny-web
docker --context macnative rm -f tiny-web
```

The server listens on the Mac's port 8080, which must be free. Its source is
in [examples/tiny-web](examples/tiny-web/README.md).

## Run a llama server with Metal

This model-free llama.cpp image runs natively on macOS and uses Metal. Keep the
GGUF in a directory on the Mac and mount it read-only into the image. See the
[image composition note](docs/LLAMA_SERVER_IMAGE.md) for its build inputs and
volume contract.

```sh
docker --context macnative pull jtstormz/tiny-web:llama-server-001
docker --context macnative run -d --name llama-server \
  -v '/absolute/path/to/go-inf-server/models/smollm2-360m:/app/models:ro' \
  jtstormz/tiny-web:llama-server-001 \
  -m models/SmolLM2-360M-Instruct-Q8_0.gguf
docker --context macnative logs llama-server
curl http://127.0.0.1:8082/health
docker --context macnative rm -f llama-server
```

Change the source of `-v` if your model directory is elsewhere. The server may
return HTTP 503 from `/health` while loading the model. It listens on the
Mac's port 8082, which must be free.

Both examples use the Mac's network directly; `-p` is not supported. Always
specify `--context macnative` for runtime commands. You can run
`docker context use macnative` to make it your default. Plain `docker ps` may
otherwise show containers managed by Docker Desktop. Pulled images remain
available after `macd` restarts.

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

Register its remote Buildx builder once:

```sh
docker buildx create --name macnative --driver remote unix:///private/tmp/macos-buildkit.sock
```

Buildx exports an OCI tarball for the runtime. The `macnative` Buildx builder
and Docker context share a name but use different sockets. The
[BuildKit guide](docs/BUILDKIT_PROTOTYPE.md) documents the patch and
Dockerfile limits. The build does not load the image into Docker Desktop.

## Kubernetes

See the separate [Kubernetes usage guide](docs/KUBERNETES.md) for scheduling a
Darwin Pod on a virtual node. Kind is the locally tested example, but the
adapter can connect to any cluster through a dedicated kubeconfig. The
[voice cluster guide](docs/VOICE_CLUSTER.md) covers the larger LiveKit setup.

## Current limits

`macd` keeps container records in memory and pulled images on disk.
The Docker API supports a narrow set of commands including `run`, `ps`,
`logs`, `stop`, and `rm`. Read-only `-v` binds and command arguments are
supported; see the [model-free llama-server example](docs/LLAMA_SERVER_BUILDPLAN.md).
`docker pull` accepts `darwin/arm64` images. There is no `docker load` or port mapping yet. The
builder's shell still resolves absolute paths on the host rather than inside
the image. The worker and runner use
macOS Seatbelt to limit ordinary file writes, while host reads and network
access remain available.

For lower-level image packaging and registry work, see the
[image packager guide](docs/IMAGE_PACKAGER.md). The
[OCI runner guide](docs/RUNNER.md) explains how to run a local tarball without
the Docker API service.

## Examples

The [tiny-web example](examples/tiny-web/README.md) is a small Go HTTP server.
With the native BuildKit worker running, build it from its Dockerfile:

```sh
docker buildx build --builder macnative --platform darwin/arm64 \
  --build-context "go-toolchain=$(go env GOROOT)" \
  --progress plain \
  --output type=oci,dest=.build/tiny-web.tar \
  examples/tiny-web
```

For this local tarball, stop the LaunchAgent with `./scripts/disable-macd.sh`
and start `macd` manually with `-image .build/tiny-web.tar -tag tiny-web:latest`
and `-runner .build/imgrun` on the usual socket. Then run
`docker --context macnative run -d --name tiny-web tiny-web:latest`. The
[Metal server example](examples/go-inf-server/README.md) has the full build
and run workflow for the cgo application and model image.
