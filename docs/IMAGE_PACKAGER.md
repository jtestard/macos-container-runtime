# `go-inf-server` image packager and local registry

The repository includes a Go standard library packager for an already-built,
Metal-backed `go-inf-server` checkout. It writes a `darwin/arm64` OCI image
layout; it does not compile the application from a Dockerfile. This path is
separate from the [Buildx example](../README.md) and its small Docker runtime.

Run the commands below from the repository root.
Replace the source path with your own checkout.

## Build the OCI layout

```sh
go run ./cmd/imgbuild \
  -source '/absolute/path/to/go-inf-server' \
  -output dist/go-inf-server-smollm2
```

The source checkout must contain `bin/server`, `lib/*.dylib`,
`config.smollm2.toml`, and
`models/smollm2-360m/SmolLM2-360M-Instruct-Q8_0.gguf`. The packager checks that
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

## Publish to the local registry

Start the registry on this Mac only:

```sh
go run ./cmd/registry -listen 127.0.0.1:5000 -data dist/registry
```

In another terminal, publish the OCI layout and inspect its tags:

```sh
go run ./cmd/imgpush -layout dist/go-inf-server-smollm2
curl http://127.0.0.1:5000/v2/go-inf-server/tags/list
```

The image is available at `127.0.0.1:5000/go-inf-server:smollm2`. Registry data
persists under `dist/registry`. This prototype supports OCI image manifests,
blob uploads using POST followed by PUT, and blob and manifest reads. It does
not yet implement the full OCI Distribution API, such as chunked uploads,
image indexes, or attestations.

## Current scope

The packager output is a local OCI layout, not an image loaded into Docker.
The packager handles regular files in this application's payload; generic
symlink and extended-attribute preservation is not implemented yet.

The existing server binary includes both a relative library search path and
an absolute fallback to its source checkout. The image retains the relative
`bin/` and `lib/` layout. The extracted application was tested on macOS 26.4.1
with its SmolLM2 model: it started on the M2 Pro Metal device, returned HTTP 200
from `/healthz`, and answered a short chat request. Running that Metal test
requires normal macOS GPU access; the restricted development-tool sandbox
cannot initialize the Metal command queue.

The image format follows the [OCI Image Specification](https://github.com/opencontainers/image-spec).
