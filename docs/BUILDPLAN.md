# Build plan: native macOS builder and first runner

**Scope:** A basic `darwin/arm64` worker for CPU shell-form `RUN`, reached
through an unmodified Docker CLI using Buildx remote. This is an integration
probe, not a complete container builder or runtime.

**Progress as of 2026-09-29:** The CPU `RUN` worker, image-contained Go build
example, local runner, and Docker CLI runtime are working. Buildx now also
compiles `go-inf-server` from source, and the native runtime has run it on
Metal. The next architectural gate is a proper image-root view for builds and
runs.

## Completed task: build `go-inf-server` from source

**Goal:** Use the existing Buildx remote worker to compile the real cgo server
from its source checkout and export a runnable `darwin/arm64` OCI image with
its llama.cpp dylibs, configuration, and SmolLM2 model.

| Step | Status | Check |
| --- | --- | --- |
| 1. Map build inputs and constraints | Done | `go.mod` requires Go 1.26; server uses cgo plus staged llama.cpp headers and dylibs; the selected GGUF is 369 MB. |
| 2. Stage a compatible Go toolchain | Done | Downloaded Go 1.26 and supplied a writable copy as a named Buildx context. |
| 3. Write the Dockerfile | Done | Selects server sources, headers, dylibs, config, and one model from the checkout; compiles with cgo in `RUN`; final stage copies only runtime payload. |
| 4. Build and inspect OCI output | Done | Buildx exported a 361 MB OCI tar; config declares `darwin/arm64`; extracted entrypoint is arm64 Mach-O; final root has only `/app` payload, with no toolchain. |
| 5. Run and document | Done | Direct runner and `docker --context macnative run -d` loaded SmolLM2 on M2 Pro Metal, offloaded 33/33 layers, served HTTP 200 from `/healthz`, and returned `pong` from chat. See [the example guide](../examples/go-inf-server/README.md). |

## Completed task: first CPU worker probe

| Step | Status | Evidence or next action |
| --- | --- | --- |
| 1. Map BuildKit's layer path | Done | Dockerfile frontend creates LLB operations; solver requests snapshots and execution; exporter diffs snapshots into OCI layers. See [BUILDKIT_PROTOTYPE.md](BUILDKIT_PROTOTYPE.md). |
| 2. Pin BuildKit and implement a Darwin worker | Done | Patch against BuildKit v0.24.0 registers a `darwin/arm64` worker using the native snapshotter, walking differ, and host shell executor. Patch applies to a pristine checkout and compiles on this Mac. |
| 3. Connect Buildx and build a CPU `RUN` | Done | `docker buildx build --builder macnative --platform darwin/arm64 --no-cache` completed against [examples/cpu-run/Dockerfile](../examples/cpu-run/Dockerfile) and exported an OCI tarball. |
| 4. Inspect the final OCI output | Done | Config declares `darwin/arm64`; one layer contains `result.txt` with `hello`. |
| 5. Record limits and handoff | Done | [BUILDKIT_PROTOTYPE.md](BUILDKIT_PROTOTYPE.md) records host absolute-path behavior and unsupported operations. Metal has since been tested with `go-inf-server`. |

## Next feasibility gates after this probe

1. Give `RUN` an image-root filesystem view while retaining macOS frameworks
   and Metal access. This determines whether normal Dockerfile path semantics
   are viable without a Linux VM.
2. Test a minimal Metal operation inside BuildKit `RUN`. The exported
   `go-inf-server` image accessed Metal at runtime; the build executor has not
   yet done so.
3. Expand Dockerfile coverage (`COPY`, base images, toolchains, ownership,
   mounts) and verify layer behavior.
4. Publish a Buildx image to the loopback registry and verify pull by digest.

## Completed probe: Kind schedules one native macOS Pod

**Goal:** Register a virtual node from a process on macOS, then run one
`darwin/arm64` image already registered in `macd` from a Deployment created
through the Kind control plane. This does not add Pod networking yet.

| Step | Status | Check |
| --- | --- | --- |
| 1. Define node and Pod contract | Done | [Kubernetes guide](KUBERNETES.md) documents placement label, taint, one-Pod limit, exact image tag and unsupported features. |
| 2. Implement Virtual Kubelet provider | Done | `kube/cmd/mackube` maps Pod creation, process status and deletion to `macd`; a validation test accepts Kubernetes' empty default security context. |
| 3. Register node with Kind | Done | `kubectl --context kind-kind get nodes` showed `macnative` Ready beside `kind-control-plane`. Adapter requires a Kind-only kubeconfig. |
| 4. Run a one-replica Deployment | Done | `kube-web:latest` built via Buildx, scheduled to `macnative`, became `1/1 Running`, and answered `ok` on host port 8081. Deployment rollout completed. |
| 5. Document limits and cleanup | Done | Deleting the Deployment removed its `macd` process and released port 8081. Service routing, Kubernetes logs, exec, probes and multiple replicas remain separate gates. |

The adapter also adopted a running Pod after its own restart. Deleting that
Deployment through the restarted adapter removed the original native
container. Stopping `macd` changed the virtual node to NotReady; restarting
`macd` returned it to Ready. These checks used the local `kind-kind` context.

The first 8080 trial exposed a still-running Metal server from an earlier run.
The Kubernetes test image now uses 8081, leaving that workload untouched.
Virtual Kubelet also resolves Kubernetes service environment variables by
default; the provider disables that step because the current runner cannot
honor environment overrides.

The first two gates are architectural. The current Seatbelt profile limits
ordinary file writes to the snapshot, but still allows host reads and system
services and does not remap `/`. The `go-inf-server` runtime test demonstrated
Metal access with these current limits.

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
