# Build plan: native macOS builder and first runner

**Scope:** A basic `darwin/arm64` worker for CPU shell-form `RUN`, reached
through an unmodified Docker CLI using Buildx remote. This is an integration
probe, not a complete container builder or runtime.

**Progress as of 2026-09-29:** The CPU `RUN` worker, image-contained Go build
example, local runner, Docker CLI `run`, request logs, and force removal are
working. The next architectural gate is a proper image-root view for builds
and runs.

| Step | Status | Evidence or next action |
| --- | --- | --- |
| 1. Map BuildKit's layer path | Done | Dockerfile frontend creates LLB operations; solver requests snapshots and execution; exporter diffs snapshots into OCI layers. See [BUILDKIT_PROTOTYPE.md](BUILDKIT_PROTOTYPE.md). |
| 2. Pin BuildKit and implement a Darwin worker | Done | Patch against BuildKit v0.24.0 registers a `darwin/arm64` worker using the native snapshotter, walking differ, and host shell executor. Patch applies to a pristine checkout and compiles on this Mac. |
| 3. Connect Buildx and build a CPU `RUN` | Done | `docker buildx build --builder macnative --platform darwin/arm64 --no-cache` completed against [examples/cpu-run/Dockerfile](../examples/cpu-run/Dockerfile) and exported an OCI tarball. |
| 4. Inspect the final OCI output | Done | Config declares `darwin/arm64`; one layer contains `result.txt` with `hello`. |
| 5. Record limits and handoff | Done | [BUILDKIT_PROTOTYPE.md](BUILDKIT_PROTOTYPE.md) records host absolute-path behavior, unsupported operations, and untested Metal access. |

## Next feasibility gates after this probe

1. Give `RUN` an image-root filesystem view while retaining macOS frameworks
   and Metal access. This determines whether normal Dockerfile path semantics
   are viable without a Linux VM.
2. Test a minimal native Metal command under the chosen execution environment.
3. Expand Dockerfile coverage (`COPY`, base images, toolchains, ownership,
   mounts) and verify layer behavior.
4. Publish a Buildx image to the loopback registry and verify pull by digest.

The first two gates are architectural. The current Seatbelt profile limits
ordinary file writes to the snapshot, but still allows host reads and system
services and does not remap `/`.

## Completed task: tiny Go web server image

**Goal:** Build a small standard-library HTTP server with a regular Dockerfile
through this Buildx remote worker, using Go staged inside the build image,
then inspect its OCI output. The binary may be run directly on the host for a
smoke test; the container runtime is not implemented yet.

| Step | Status | Check |
| --- | --- | --- |
| 1. Stage Go inside the build image | Done | `COPY --from=go-toolchain` places the macOS Go distribution in the build stage; `RUN` invokes that staged `bin/go` with staged `GOROOT`. |
| 2. Build through Buildx remote | Done | Buildx exported a multi-stage OCI image from [examples/tiny-web/Dockerfile](../examples/tiny-web/Dockerfile). |
| 3. Inspect and smoke test | Done | Final image is `darwin/arm64` with two layers and only an arm64 Mach-O server; extracted server returned `ok` at `/healthz`. |
| 4. Document exact commands and limits | Done | See [examples/tiny-web/README.md](../examples/tiny-web/README.md). |

The first draft compiled with a host Go binary passed as a build argument. It
proved `COPY` and native CPU execution, but did not meet the requirement that
the compiler be in the build image. The revised example copies Go into the
build stage before compilation. Go is sourced from this Mac as a named build
context; a reusable Go base image is a later registry-backed step.

## Completed task: local runner for the tiny web image

**Goal:** Launch the built `darwin/arm64` OCI tarball on this Mac without a
Linux VM. The first runner targets the tiny server's relative entrypoint and
working directory. It will materialize the image, execute the native binary,
forward output and signals, and clean up after exit.

| Step | Status | Check |
| --- | --- | --- |
| 1. Read image metadata and verify blobs | Done | Selects the Darwin arm64 manifest and validates manifest, config, layer, and uncompressed layer digests. |
| 2. Apply layers to a temporary root | Done | Recreates the tiny image's regular files and directories, rejecting unsupported entries. |
| 3. Launch and supervise the process | Done | Resolves `./server` under `/app`, forwards output and signals, returns process status, and cleans up. |
| 4. End-to-end test and document | Done | Runner started the image-built server; `/healthz` returned `ok`; Ctrl-C stopped it and the temporary filesystem was removed. See [RUNNER.md](RUNNER.md). |

## Completed task: Docker CLI `run` integration

**Goal:** Make the unmodified Docker CLI launch the tiny image through a
host-local Docker Engine API socket. `docker --context macnative run` works
for an image registered when the service starts. The OCI runner remains
responsible for materializing and executing the image.

| Step | Status | Check |
| --- | --- | --- |
| 1. Discover the CLI request sequence | Done | The installed CLI opens attach and wait streams before start; wait headers must be sent immediately. |
| 2. Implement a minimal Engine API service | Done | Unix socket, version negotiation, image lookup, create/start, attach, logs, wait, stop, inspect, and cleanup for the tiny image. |
| 3. Register a Docker context | Done | `macnative` points at the runtime socket; the default context remains `desktop-linux`. |
| 4. End-to-end run | Done | Foreground and detached `docker --context macnative run` launched the server; `/healthz` returned `ok`; `ps`, `logs`, `stop`, and `rm` worked. `docker image inspect` reports the OCI config digest. |
| 5. Daemon shutdown | Done | SIGTERM stopped a running server and released host port 8080. The service was restarted with no test container active. See [DOCKER_RUNTIME.md](DOCKER_RUNTIME.md). |

## Completed fix: force-remove a running container

The Docker CLI sends `DELETE /containers/{id}?force=1` for `docker rm -f`.
The service now handles force removal of a running container. The specific
container from the original report was stopped and removed using the existing
two-command path before this fix.

| Step | Status | Check |
| --- | --- | --- |
| 1. Handle force removal | Done | The runner kills its child process group and cleans up before the daemon deletes the record. |
| 2. Verify through Docker CLI | Done | `docker --context macnative rm -f ff03a037e4f5` succeeded while the server was running; the record and port 8080 are gone. |
| 3. Document and commit | Done | Runtime guide updated; implementation committed separately from request logging. |

## Completed task: request logging and Docker log commands

**Goal:** Make the tiny server log every HTTP request, rebuild its image, and
verify those messages through the regular Docker CLI.

| Step | Status | Check |
| --- | --- | --- |
| 1. Add request logging | Done | Each request logs method, request URI, and client address. |
| 2. Rebuild and register the image | Done | Buildx exported a new `darwin/arm64` OCI tar, and `macd` was restarted with it. |
| 3. Support live Docker logs | Done | `docker logs -f` streamed a new request and exited after the container was removed. |
| 4. End-to-end commands | Done | `run`, `/healthz`, `logs`, `logs -f`, `rm -f`, and `ps -a` worked; port 8080 was released. |
