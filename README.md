# macOS OCI image experiment

The [build plan](BUILDPLAN.md) tracks progress and next gates. The proposed
system design and unresolved decisions are in [ARCHITECTURE.md](ARCHITECTURE.md).

This repository packages a `darwin/arm64` OCI image for the native
Metal-backed `go-inf-server` application and serves it from a small local
registry. A pinned [BuildKit worker experiment](BUILDKIT_PROTOTYPE.md) now
accepts Buildx remote builds and runs a simple CPU `RUN` on macOS arm64. Its
host-shell execution does not yet provide Dockerfile root filesystem semantics.
A [local OCI runner](RUNNER.md) launches the tiny web image, and a
[Docker CLI runtime prototype](DOCKER_RUNTIME.md) exposes it through
`docker --context macnative run`. This is a narrow Engine API subset.

## Build the image

The packager uses only the Go standard library. It reads an already-built
`go-inf-server` checkout and writes an OCI image layout:

```sh
go run ./cmd/imgbuild \
  -source '/absolute/path/to/go-inf-server' \
  -output dist/go-inf-server-smollm2
```

The source checkout must contain `bin/server`, `lib/*.dylib`,
`config.smollm2.toml`, and
`models/smollm2-360m/SmolLM2-360M-Instruct-Q8_0.gguf`. The builder checks that
the executable and libraries are arm64 Mach-O files. It refuses to overwrite
an existing output directory.

The resulting layout has `oci-layout`, `index.json`, and SHA-256-addressed blobs.
The index selects `darwin/arm64` and names the image
`go-inf-server:smollm2`. Its manifest refers to two gzip-compressed tar layers:
the application files and the GGUF model. The image config records the
uncompressed layer digests, entrypoint, arguments, and working directory.

The model is included for a self-contained first image. Other models, the web
application, the speech services, source code, logs, and local configuration
files outside the SmolLM2 profile are excluded.

## Local registry prototype

Run the registry on this Mac only:

```sh
go run ./cmd/registry -listen 127.0.0.1:5000 -data dist/registry
```

In another terminal, publish the existing OCI layout:

```sh
go run ./cmd/imgpush -layout dist/go-inf-server-smollm2
curl http://127.0.0.1:5000/v2/go-inf-server/tags/list
```

The image is available at `127.0.0.1:5000/go-inf-server:smollm2`. Registry data
persists under `dist/registry`. This prototype supports OCI image manifests,
blob uploads using POST followed by PUT, and blob and manifest reads. It does
not yet implement the full OCI Distribution API, such as chunked uploads,
image indexes, or attestations.

## Builder direction

The target workflow is an ordinary Dockerfile passed from the unmodified
Docker CLI through Buildx remote to a host-local BuildKit service. The first
CPU `RUN` probe works; details and reproduction steps are in
[BUILDKIT_PROTOTYPE.md](BUILDKIT_PROTOTYPE.md). The
[tiny Go web server example](examples/tiny-web/README.md) stages a macOS Go
distribution inside the build image and compiles with that copy. A normal
image-root view, Metal test, registry publishing, and building `go-inf-server`
from source remain feasibility gates. See [ARCHITECTURE.md](ARCHITECTURE.md)
for the design.

## Current scope

The packager output is a local OCI layout, not an image loaded into Docker.
The Buildx worker currently exports an OCI tarball. The local runner launches
the tiny server from that tarball, and the small Docker API service supports
`docker run` for it. General Dockerfile and Docker Engine compatibility remain
future work. The image
packager handles regular files in this
application's payload; generic symlink and extended-attribute preservation is
not implemented yet.

The existing server binary includes both a relative library search path and
an absolute fallback to its source checkout. The image retains the relative
`bin/` and `lib/` layout. The extracted application was tested on macOS 26.4.1
with its SmolLM2 model: it started on the M2 Pro Metal device, returned HTTP 200
from `/healthz`, and answered a short chat request. Running that Metal test
requires normal macOS GPU access; the restricted development-tool sandbox
cannot initialize the Metal command queue.

The image format follows the [OCI Image Specification](https://github.com/opencontainers/image-spec).
