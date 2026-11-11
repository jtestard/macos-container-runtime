# Voice stack images

The dedicated `livekit` Kind cluster runs LiveKit and the web UI as Linux/arm64
images. Four services run as native `darwin/arm64` processes through `macd`
and `mackube`. See [VOICE_CLUSTER.md](VOICE_CLUSTER.md) for deployment and
ports.

Three of the native images are third-party servers, built and published by the
[macos-native-images](https://github.com/jtestard/macos-native-images)
repository; pull them, there is nothing to build here:

| Service | Image |
| --- | --- |
| LLM | `jtstormz/llama-server:ee0445c` (model mounted from the Mac, see [LLAMA_SERVER_IMAGE.md](LLAMA_SERVER_IMAGE.md)) |
| Speech-to-text | `jtstormz/whisper-server:v1.9.1-base.en` |
| Text-to-speech | `jtstormz/kokoro:b4ef64b` |

This page builds the voice application's own two images: the web UI (Linux)
and the voice agent (`darwin/arm64`). The BuildKit worker and the
`macnative-build` Buildx builder are set up in the [main README](../README.md).
Build outputs go to the ignored `.build` directory.

## Prepare build contexts

The web and agent sources, and the agent's `speech.env`, come from the voice
application checkout; set `APP_SOURCE` to its path. The verified build used
its commit `351a70a` with Node v20.11.1 and npm 10.2.4.

```sh
APP_SOURCE="/path/to/voice-app"
mkdir -p .build/voice-web-context/cmd .build/voice-agent-context/agent \
  .build/node-toolchain/bin .build/node-toolchain/lib/node_modules \
  .build/voice-launch
cp "$APP_SOURCE/go.mod" "$APP_SOURCE/go.sum" .build/voice-web-context/
rsync -a --delete --exclude node_modules --exclude dist \
  "$APP_SOURCE/cmd/web/" .build/voice-web-context/cmd/web/
rsync -a --delete "$APP_SOURCE/agent/src/" .build/voice-agent-context/agent/src/
cp "$APP_SOURCE/agent/package.json" "$APP_SOURCE/agent/package-lock.json" \
  "$APP_SOURCE/agent/tsconfig.json" .build/voice-agent-context/agent/
cp "$APP_SOURCE/speech.env" .build/voice-agent-context/
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
  go build -o .build/voice-launch/launch ./examples/voice-cluster/launcher
```

The agent context expects a macOS arm64 Node distribution with
`bin/node` and `lib/node_modules/npm/`. Set `NODE_HOME` to its extracted
distribution root, then stage it:

```sh
cp "$NODE_HOME/bin/node" .build/node-toolchain/bin/node
rsync -a "$NODE_HOME/lib/node_modules/npm/" \
  .build/node-toolchain/lib/node_modules/npm/
```

The Node binary and npm tree must come from the same distribution.

## Build

```sh
docker --context desktop-linux buildx build --platform linux/arm64 \
  -t macvoice-web:local -f examples/voice-cluster/web/Dockerfile \
  --load .build/voice-web-context

docker buildx build --builder macnative-build --platform darwin/arm64 \
  --build-context "node-toolchain=$PWD/.build/node-toolchain" \
  --build-context "agent-source=$PWD/.build/voice-agent-context" \
  --build-context "voice-launcher=$PWD/.build/voice-launch" \
  --progress plain --output type=oci,dest=.build/voice-agent.tar \
  examples/voice-cluster/agent
```

The native builder executes `RUN` in writable host snapshots, so the agent
Dockerfile calculates snapshot paths for its staged Node. The runtime extracts
each image under a fresh temporary root, and the agent image uses the small Go
[launcher](../examples/voice-cluster/launcher/main.go) to resolve that root at
startup. The agent build downloads its local VAD and turn detector files.
