# llama-server image composition

This is the handoff for the **model-free** native llama.cpp server image. The
build recipe is [examples/llama-server/Dockerfile](../examples/llama-server/Dockerfile);
the executable build and run commands are in the
[build plan](LLAMA_SERVER_BUILDPLAN.md).

## Build inputs and stages

| Input | Use | Present in final image? |
| --- | --- | --- |
| `llama-source` named context | Staged llama.cpp source from the sibling `go-inf-server/llama.cpp` checkout; verified at commit `ee0445c`. Top-level model files are excluded, while source headers under `tools/mtmd/models` are retained. | No |
| `cmake-toolchain` named context | macOS CMake distribution, copied into the build stage at `/cmake`. | No |
| Host Xcode compiler, SDK, and `make` | Used by the current native BuildKit worker during `RUN`. The worker executes in a writable host snapshot and references these host paths directly. | No |
| GGUF model | Not a build input. | No |

The `build` stage starts from `scratch`, copies the source and CMake contexts,
then compiles the `llama-server` target in Release mode. It sets
`BUILD_SHARED_LIBS=OFF`, `GGML_METAL=ON`, and
`GGML_METAL_EMBED_LIBRARY=ON`. OpenSSL, the web UI, tests, examples, and the
unified llama app are disabled. `LLAMA_BUILD_TOOLS=ON` is required by this
source revision for CMake to define the server target.

The final stage also starts from `scratch`. It copies only the resulting
Mach-O arm64 `llama-server` binary into `/app/bin/llama-server`. The Metal
library is embedded in that binary; its remaining dynamic links are macOS
system libraries and frameworks, including Metal and Accelerate. Neither the
source tree, build tools, dylibs from Homebrew, nor model weights are copied.

## Verified OCI contents

The local artifact is `.build/llama-server.tar`, an OCI image for
`darwin/arm64`, about **7.3 MB** as a tarball. Inspection of its two filesystem
layers found one regular file in total: `app/bin/llama-server`. The other
layer contains the `app` directory entry; no layer contains a
GGUF. The image configuration sets:

| Field | Value |
| --- | --- |
| Working directory | `/app` |
| Entrypoint | `./bin/llama-server --host 127.0.0.1 --port 8082 --n-gpu-layers 99` |
| Default command | None; the caller supplies `-m` and a model path |
| Exposed port metadata | `8082/tcp` |

The image is published in the existing private Docker Hub repository as
`jtstormz/tiny-web:llama-server-001`, OCI index digest
`sha256:ce40c59fa3cba71fce515d6dc5be37fd17ae6c97482e74f0ddfa8c0bb577ad05`.
The index contains a `darwin/arm64` runnable manifest. The repository name is
shared with other images because this Docker Hub plan does not permit another
private repository.

## Runtime model contract

`macd` registers the OCI tarball as one image. At container creation it accepts
command arguments and read-only `-v` binds. For example:

```sh
MODEL_DIR='/absolute/path/to/your/model-directory'
docker -H unix:///private/tmp/macnative-llama-docker.sock run -d \
  --name llama-server \
  -v "$MODEL_DIR:/app/models:ro" \
  llama-server:local -m models/SmolLM2-360M-Instruct-Q8_0.gguf
```

The host directory is **not copied into the OCI image**. `imgrun` extracts the
image into a temporary root and links `/app/models` there to the existing host
directory. Its Seatbelt profile denies writes outside the temporary run
state. The current runner does not remap absolute image paths, so the model
argument must be relative to `/app`, as shown. The mount target must be absent
from the image. `:rw`, Docker `--mount`, and port mapping are unsupported.

This volume-backed image was verified with SmolLM2: the process opened the
GGUF at its host path, `/health` returned `{"status":"ok"}`, and
`/v1/chat/completions` returned a response. The process loaded the Apple AGX
Metal driver. The exact number of model layers offloaded to Metal was not
measured.

The Kubernetes adapter in `kube/cmd/mackube` passes container `args` and one
read-only `hostPath` directory mount to `macd`. The
[voice cluster LLM Deployment](../examples/voice-cluster/go-inf-server.yaml)
uses this image, mounts SmolLM2 from the Mac, and adds `--port 8080` so the
existing voice agent can keep its `LLM_BASE_URL`. `macd` registers only one
image per daemon and does not pull images itself.

The older `jtstormz/tiny-web:llama-server-smollm2-001` tag **does** bundle
SmolLM2. It is an explicit historical variant, not the default composition.
