# llama-server image

`jtstormz/llama-server:ee0445c` is upstream llama.cpp's `llama-server`, built
for `darwin/arm64` with Metal and **no model bundled**. It is built and
published from the
[macos-native-images](https://github.com/jtestard/macos-native-images)
repository, which pins the llama.cpp commit (`ee0445c`) in its Makefile.

## Image contents

The image holds one file, the static `app/bin/llama-server` Mach-O binary with
the Metal library embedded; its remaining dynamic links are macOS system
libraries and frameworks. The tarball is about 8 MB. The image configuration
sets:

| Field | Value |
| --- | --- |
| Working directory | `/app` |
| Entrypoint | `./bin/llama-server --host 127.0.0.1 --port 8082 --n-gpu-layers 99` |
| Default command | None; the caller supplies `-m` and a model path |
| Exposed port metadata | `8082/tcp` |

## Model contract

Mount a host directory containing a GGUF read-only at `/app/models` and pass
a **relative** model path:

```sh
MODEL_DIR="$HOME/models/smollm2-360m"
docker pull jtstormz/llama-server:ee0445c
docker run -d --name llama-server \
  -v "$MODEL_DIR:/app/models:ro" \
  jtstormz/llama-server:ee0445c -m models/SmolLM2-360M-Instruct-Q8_0.gguf
```

The host directory is **not copied into the image**. `imgrun` extracts the
image into a temporary root and links `/app/models` there to the existing host
directory. Its Seatbelt profile denies writes outside the temporary run state.
The runner does not remap absolute image paths, so the model argument must be
relative to `/app`, as shown. The mount target must be absent from the image.
`:rw`, Docker `--mount`, and port mapping are unsupported. Arguments after the
image name are appended to the entrypoint; a later `--port` overrides 8082.
`/health` returns HTTP 503 while the model loads.

The Kubernetes adapter in `kube/cmd/mackube` passes container `args` and one
read-only `hostPath` directory mount to `macd`. The
[standalone llama example](KUBERNETES.md) uses this image and mounts SmolLM2
from the Mac. The
[voice cluster LLM Deployment](../examples/voice-cluster/llama-server.yaml)
defaults to Mistral 7B Instruct and adds `--port 8080` so the voice agent can
keep its `LLM_BASE_URL`. Pull the image into `macd` before creating the Pod;
the adapter does not pull images.

## Earlier tags

Before the images moved to their own repositories, this image was published as
`jtstormz/tiny-web:llama-server-001`, built from the same llama.cpp commit. The
`jtstormz/tiny-web:llama-server-smollm2-001` tag bundles SmolLM2 and is a
historical variant.
