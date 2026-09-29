# Native BuildKit worker experiment

This is a pinned BuildKit v0.24.0 patch for macOS arm64. It registers a
Darwin worker, accepts Buildx remote connections through a local Unix socket,
executes a simple CPU shell-form `RUN`, and exports an OCI image. It is a
feasibility probe, not yet a general Dockerfile builder.

## How BuildKit makes layers

The Dockerfile frontend translates instructions into an LLB graph. `FROM`
provides a source, `COPY` becomes a file operation, and `RUN` becomes an exec
operation. The solver checks cache keys for each operation and asks a worker
to execute operations whose results are missing. For `RUN`, the worker prepares
a writable snapshot, the executor runs the command, and BuildKit commits the
result. During export, the differ compares that snapshot with its parent and
emits a filesystem changeset as an OCI layer. BuildKit then writes the image
configuration and manifest. Metadata instructions such as `ENV` and `CMD`
change configuration without necessarily adding a filesystem layer. Cached
operations can skip execution.

This experiment uses containerd's native directory snapshotter, walking
differ, and file applier, together with BuildKit's Dockerfile frontend, solver,
cache, and OCI exporter.

## Reproduce

Prerequisites: macOS arm64, Go 1.24 or later, Docker CLI with Buildx, and Git.
Run these commands from this repository's root. BuildKit source is cloned into
ignored `.build/`; the reviewable change is
[`patches/buildkit-v0.24.0-darwin-prototype.patch`](../patches/buildkit-v0.24.0-darwin-prototype.patch).

```sh
mkdir -p .build
git clone --branch v0.24.0 --depth 1 https://github.com/moby/buildkit.git .build/buildkit-src
git -C .build/buildkit-src apply ../../patches/buildkit-v0.24.0-darwin-prototype.patch
cd .build/buildkit-src
GOCACHE="$PWD/../go-cache" go build -mod=vendor -o ../buildkitd-macos ./cmd/buildkitd
cd ../..
```

Start the daemon in one terminal:

```sh
.build/buildkitd-macos \
  --root .build/buildkit-state \
  --addr unix:///private/tmp/macos-buildkit.sock \
  --otel-socket-path /private/tmp/macos-buildkit-trace.sock \
  --containerd-worker=false
```

In another terminal, register the Buildx remote builder once, then build the
sample Dockerfile:

```sh
docker buildx create --name macnative --driver remote unix:///private/tmp/macos-buildkit.sock
docker buildx build --builder macnative --platform darwin/arm64 \
  --no-cache --progress plain \
  --output type=oci,dest=.build/cpu-run.tar examples/cpu-run
```

The exported OCI image has one layer containing `result.txt` with `hello`.
The platform in its image configuration is `darwin/arm64`. Buildx remote
connects to the native BuildKit daemon; Docker Desktop does not perform this
build. This command exports an OCI tarball and does not load an image into
Docker Desktop or publish it to the local registry.

## Exact limits of the current worker

The `RUN` executor invokes the host `/bin/sh` under macOS Seatbelt with its
working directory inside a writable snapshot. Seatbelt permits file writes
within that snapshot and denies them elsewhere; reads and system services are
broadly available. This is limited write confinement, not full containment.
The executor does **not** remap `/` to the image root: absolute paths and shell
tools still resolve on the host. `FROM scratch` works in this probe because
the host supplies the shell. That departs from normal Dockerfile semantics.
Use only trusted commands that write relative paths in the snapshot.

The patch rejects extra `RUN` mounts, non-root `USER`, read-only roots, and
non-shell-form commands. It bypasses macOS bind-mount calls in BuildKit and
containerd, so read-only mount semantics are not enforced. It also skips
transferred-file `lchown`, so ownership is not preserved. Those vendor and
mount changes are exploratory compatibility workarounds. Secrets, cache
mounts, network policy, symlinks, xattrs, and deletion whiteouts have not been
validated. Basic `COPY` and a staged Go compile have been validated in
[examples/tiny-web](../examples/tiny-web/README.md). Metal access from this executor has
not been tested. A host shell may be able to reach Metal, but the result will
depend on macOS permissions and any later isolation mechanism.

The next feasibility gate is an image-root view that retains access to macOS
frameworks and Metal while ensuring normal Dockerfile path semantics. Until
that exists, the patch should not be treated as a general-purpose builder.

## References

- [BuildKit Dockerfile to LLB](https://github.com/moby/buildkit/blob/master/docs/dev/dockerfile-llb.md)
- [BuildKit solver](https://github.com/moby/buildkit/blob/master/docs/dev/solver.md)
- [OCI layer changesets](https://github.com/opencontainers/image-spec/blob/main/layer.md)
