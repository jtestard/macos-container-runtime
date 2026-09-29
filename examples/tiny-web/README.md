# Tiny Go web server image

This example builds a macOS arm64 Go HTTP server through the native BuildKit
worker. The build stage contains a Go distribution; `RUN` invokes the Go binary
copied into that stage. The final stage contains only the server binary.

## Build

Start the patched native `buildkitd` and register the `macnative` Buildx remote
builder as described in [BUILDKIT_PROTOTYPE.md](../../BUILDKIT_PROTOTYPE.md).
Use an installed macOS arm64 Go 1.24 distribution as the named build context:

```sh
mkdir -p .build
docker buildx build --builder macnative --platform darwin/arm64 \
  --build-context "go-toolchain=$(go env GOROOT)" \
  --progress plain \
  --output type=oci,dest=.build/tiny-web.tar \
  examples/tiny-web
```

Run this command from the repository root. The `go-toolchain` context is copied
into `/toolchain` in the build stage before `RUN`. `RUN` sets `GOROOT` to the
staged copy, uses its `bin/go`, disables network module lookup and VCS stamping,
and keeps Go's cache and temporary files inside the snapshot. On this Mac,
transferring the complete Go distribution sent about 252 MB of build context.
BuildKit can cache the copy, but the distribution must still be supplied to
the build command. A future reusable Darwin Go base image could replace this
named context once registry-backed base images are validated.

The exported OCI tar has platform `darwin/arm64`, working directory `/app`,
entrypoint `./server`, and two final image layers: an `/app` directory and the
arm64 Mach-O `app/server` binary. The Go distribution is absent from the final
image because it is used only in the build stage.

This prototype's `RUN` still uses the host shell and resolves absolute paths
on the host. The Dockerfile derives the physical snapshot path to locate the
staged Go distribution. This workaround is specific to the current worker;
normal Dockerfile root filesystem semantics are still a separate design gate.
The image can be launched through the project's small
[local OCI runner](../../RUNNER.md) or through the
[Docker CLI runtime prototype](../../DOCKER_RUNTIME.md). The runtime service
supports `docker --context macnative run tiny-web:latest` for this registered
image. `GET http://127.0.0.1:8080/healthz` returned `ok`.
The server logs the method, URI, and client address of each HTTP request;
the messages are available through `docker --context macnative logs tiny-web`.
