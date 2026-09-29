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
repository and build the two runtime programs:

```sh
git clone https://github.com/jtestard/macos-container-runtime.git
cd macos-container-runtime
mkdir -p .build
go build -o .build/imgrun ./cmd/imgrun
go build -o .build/macd ./cmd/macd
docker context create macnative \
  --docker host=unix:///private/tmp/macnative-docker.sock
```

Create the Docker context only once. `imgrun` opens an OCI image and launches
its macOS program; `macd` serves the Docker API over the local Unix socket.

## Run an image from Docker Hub

The runtime currently accepts an OCI tarball, with one image registered when
`macd` starts. It does not yet implement `docker pull`, so use
[`crane`](https://github.com/google/go-containerregistry/tree/main/cmd/crane)
to download a Darwin image as an OCI layout. Install it with
`brew install crane`, then run:

```sh
IMAGE=jtstormz/tiny-web:go-inf-server-smollm2-001
LAYOUT=$(mktemp -d .build/hub-image.XXXXXX)
crane pull --platform darwin/arm64 --format=oci "$IMAGE" "$LAYOUT"
tar --format=ustar -cf .build/hub-image.tar -C "$LAYOUT" oci-layout index.json blobs
.build/imgrun -inspect -image .build/hub-image.tar
```

The inspection command checks the image before starting it and should report
`"os":"darwin"` and `"architecture":"arm64"`. The SmolLM2 image includes a
model, so downloading and starting it may take time.

Start the Docker API service in one terminal:

```sh
.build/macd -image .build/hub-image.tar -tag "$IMAGE" \
  -runner .build/imgrun -socket /private/tmp/macnative-docker.sock
```

In another terminal, run and inspect the image with the regular Docker CLI:

```sh
docker --context macnative run -d --name go-inf-server \
  jtstormz/tiny-web:go-inf-server-smollm2-001
docker --context macnative ps
docker --context macnative logs go-inf-server
curl http://127.0.0.1:8080/healthz
docker --context macnative rm -f go-inf-server
```

The server binds directly to the Mac's port 8080; `-p` is not supported. Always
specify `--context macnative` for runtime commands. You can run
`docker context use macnative` to make it your default. Plain `docker ps` may otherwise
show containers managed by Docker Desktop. To run a different image, stop
`macd`, pull and package that image, then restart `macd` with its tarball and
tag. For example, `jtstormz/tiny-web:dev-00` can be substituted for `IMAGE`
when that tag is available on Docker Hub.

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

`macd` keeps container records in memory and serves one OCI tarball at a time.
The Docker API supports a narrow set of commands including `run`, `ps`,
`logs`, `stop`, and `rm`. There is no native image pull, `docker load`, volume
mounting, or port mapping yet. The builder's shell still resolves absolute
paths on the host rather than inside the image. The worker and runner use
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

Start `macd` with `-image .build/tiny-web.tar -tag tiny-web:latest`, then run
`docker --context macnative run -d --name tiny-web tiny-web:latest`. The
[Metal server example](examples/go-inf-server/README.md) has the full build
and run workflow for the cgo application and model image.
