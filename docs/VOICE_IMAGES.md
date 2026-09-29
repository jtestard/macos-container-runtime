# Voice stack images

The dedicated `livekit` Kind cluster runs LiveKit and the web UI as Linux/arm64
images. The four model and agent services run from `darwin/arm64` OCI images
through the local BuildKit worker, `imgrun`, `macd`, and `mackube`. See
[VOICE_CLUSTER.md](VOICE_CLUSTER.md) for deployment and ports. The BuildKit
worker and Buildx builder are set up in the [main README](../README.md).

Build outputs go to the ignored `.build` directory. The sibling
`go-inf-server` checkout supplies the web and agent source, speech settings,
Kokoro source and weights, and Whisper model. The LLM now uses the model-free
[llama-server image](LLAMA_SERVER_IMAGE.md); its GGUF stays on the Mac and is
mounted when the Pod starts. See the [llama build plan](LLAMA_SERVER_BUILDPLAN.md)
for its build and published image tag.
The verified build used `go-inf-server` commit `351a70a`, its Kokoro checkout
at `b4ef64b`, Whisper v1.9.1 at `f049fff`, and Node v20.11.1 with npm 10.2.4.

## Prepare build contexts

The named contexts below make the toolchains and application sources explicit.
They are build inputs, not paths the final image uses on the host. Replace the
source path with your own checkout.

```sh
SOURCE="/absolute/path/to/go-inf-server"
mkdir -p .build/voice-web-context/cmd .build/voice-agent-context/agent \
  .build/node-toolchain/bin .build/node-toolchain/lib/node_modules \
  .build/uv-toolchain .build/voice-launch
cp "$SOURCE/go.mod" "$SOURCE/go.sum" .build/voice-web-context/
rsync -a --delete --exclude node_modules --exclude dist \
  "$SOURCE/cmd/web/" .build/voice-web-context/cmd/web/
rsync -a --delete "$SOURCE/agent/src/" .build/voice-agent-context/agent/src/
cp "$SOURCE/agent/package.json" "$SOURCE/agent/package-lock.json" \
  "$SOURCE/agent/tsconfig.json" .build/voice-agent-context/agent/
cp "$SOURCE/speech.env" .build/voice-agent-context/
rsync -a --delete --exclude .git --exclude .venv --exclude node_modules \
  --exclude '*.pth' \
  "$SOURCE/third_party/Kokoro-FastAPI/" .build/kokoro-source/
cp -L "$(command -v uv)" .build/uv-toolchain/uv
go build -o .build/voice-launch/launch ./examples/voice-cluster/launcher
```

The agent context expects a macOS arm64 Node distribution with
`bin/node` and `lib/node_modules/npm/`. The build on this Mac used Node
v20.11.1. Set `NODE_HOME` to its extracted distribution root, then stage it:

```sh
cp "$NODE_HOME/bin/node" .build/node-toolchain/bin/node
rsync -a "$NODE_HOME/lib/node_modules/npm/" \
  .build/node-toolchain/lib/node_modules/npm/
```

The Node binary and npm tree must come from the same distribution.

Clone the pinned Whisper source and stage a macOS CMake distribution:

```sh
git clone --branch v1.9.1 --depth 1 \
  https://github.com/ggml-org/whisper.cpp.git .build/whisper-src
```

The `cmake-toolchain` context must contain `bin/cmake` and its resource tree.
Set `CMAKE_TOOLCHAIN` to the `CMake.app/Contents` directory on your Mac.
The Whisper Dockerfile currently names the Xcode compiler paths on this Mac;
adjust them if Xcode is installed elsewhere.

## Build

```sh
CMAKE_TOOLCHAIN='/absolute/path/to/CMake.app/Contents'
docker --context desktop-linux buildx build --platform linux/arm64 \
  -t macvoice-web:local -f examples/voice-cluster/web/Dockerfile \
  --load .build/voice-web-context

docker buildx build --builder macnative --platform darwin/arm64 \
  --build-context "whisper-source=$PWD/.build/whisper-src" \
  --build-context "whisper-model=$SOURCE/models/whisper" \
  --build-context "cmake-toolchain=$CMAKE_TOOLCHAIN" \
  --progress plain --output type=oci,dest=.build/whisper-base-en.tar \
  examples/voice-cluster/whisper

docker buildx build --builder macnative --platform darwin/arm64 \
  --build-context "node-toolchain=$PWD/.build/node-toolchain" \
  --build-context "agent-source=$PWD/.build/voice-agent-context" \
  --build-context "voice-launcher=$PWD/.build/voice-launch" \
  --progress plain --output type=oci,dest=.build/voice-agent.tar \
  examples/voice-cluster/agent

docker buildx build --builder macnative --platform darwin/arm64 \
  --build-context "uv-toolchain=$PWD/.build/uv-toolchain" \
  --build-context "kokoro-source=$PWD/.build/kokoro-source" \
  --build-context "kokoro-weights=$SOURCE/third_party/Kokoro-FastAPI/api/src/models/v1_0" \
  --build-context "voice-launcher=$PWD/.build/voice-launch" \
  --progress plain --output type=oci,dest=.build/kokoro-mps.tar \
  examples/voice-cluster/kokoro
```

The native builder executes `RUN` in writable host snapshots, so these
Dockerfiles calculate snapshot paths for staged compilers. The runtime extracts
each image under a fresh temporary root. The agent and Kokoro images use a
small Go launcher to resolve that root at startup. The agent build downloads
its local VAD and turn detector files. The Kokoro build downloads a managed
Python 3.12 distribution and its Python packages. The Kokoro dependency set
is currently resolved from upstream version ranges at build time; the image
itself is fixed once exported, but rebuilding later can select newer package
versions.
