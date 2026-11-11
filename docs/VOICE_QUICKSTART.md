# Voice assistant quickstart

Talk to a voice assistant in your browser, with every model running on your
Mac's GPU. This page goes from a fresh clone to a working conversation using
published images; nothing is built except the runtime itself.

What runs where:

- **Kubernetes (Kind, in Docker Desktop):** the LiveKit media server and the
  web page you open.
- **Natively on macOS, scheduled by Kubernetes:** Mistral 7B through
  llama.cpp, Whisper speech-to-text, Kokoro text-to-speech, and the voice
  agent that connects them. These run as macOS processes with Metal, not in a
  Linux VM.

## Requirements

- An Apple Silicon Mac with at least 16 GB of memory and about 8 GB of free
  disk space (a 4.4 GB model and about 1.2 GB of images).
- [Docker Desktop](https://www.docker.com/products/docker-desktop/), running.
- Git, Go 1.26 or later, Kind and kubectl:

  ```sh
  brew install go kind kubectl
  ```

- These ports free on the Mac: 20082–20085, 20250, 30880–30882 and 30900.

## 1. Install the runtime

```sh
git clone https://github.com/jtestard/macos-container-runtime.git
cd macos-container-runtime
./install.sh
./scripts/enable-macd.sh
```

This installs `macd`, the native container runtime, as a LaunchAgent and adds
a `macnative` Docker context. Your default Docker context does not change.

## 2. Pull the native images

```sh
for image in jtstormz/llama-server:ee0445c-2 jtstormz/whisper-server:v1.9.1-base.en-2 \
    jtstormz/kokoro:b4ef64b-2 jtstormz/voice-agent:0.1.3; do
  docker --context macnative pull "$image"
done
```

## 3. Download the language model

```sh
MODEL_DIR="$HOME/models/mistral-7b"
mkdir -p "$MODEL_DIR"
curl -fL -o "$MODEL_DIR/Mistral-7B-Instruct-v0.3-Q4_K_M.gguf" \
  https://huggingface.co/bartowski/Mistral-7B-Instruct-v0.3-GGUF/resolve/main/Mistral-7B-Instruct-v0.3-Q4_K_M.gguf
```

The model stays on your Mac and is mounted read-only into the LLM.

## 4. Create the cluster

```sh
KIND_CONFIG=examples/voice-cluster/livekit/kind-cluster.yaml ./scripts/kube-up.sh livekit
```

This creates a Kind cluster named `livekit` in Docker Desktop and joins your
Mac to it as a node named `macnative`. It takes a minute or two the first time.

## 5. Deploy the services

```sh
kubectl --context kind-livekit apply -k examples/voice-cluster/livekit
kubectl --context kind-livekit apply -f examples/voice-cluster/web.yaml
sed "s|__MODEL_DIR__|$MODEL_DIR|g" examples/voice-cluster/llama-server.yaml \
  | kubectl --context kind-livekit apply -f -
kubectl --context kind-livekit apply \
  -f examples/voice-cluster/whisper.yaml -f examples/voice-cluster/kokoro.yaml
```

Wait until the LLM and text-to-speech have loaded their models, then start the
agent:

```sh
until curl -sf http://127.0.0.1:20082/health >/dev/null \
    && curl -sf http://127.0.0.1:20085/health >/dev/null; do sleep 5; done
kubectl --context kind-livekit apply -f examples/voice-cluster/agent.yaml
```

## 6. Talk to it

Open **http://localhost:30900**, select **Start conversation**, and allow the
microphone. The assistant greets you; ask it something. Both sides of the
conversation appear on the page as they are transcribed.

To see what is running:

```sh
kubectl --context kind-livekit -n livekit get pods -o wide
```

## Stop

```sh
./scripts/kube-down.sh livekit
```

This stops the native processes and deletes the cluster. The images and the
model stay, so steps 4 to 6 are enough to start again. To remove the runtime
itself, run `./scripts/disable-macd.sh`.

## If something goes wrong

- **A Pod is not `Running`:** read its logs, for example
  `kubectl --context kind-livekit -n livekit logs deployment/llama-server`.
- **A port is already in use:** `lsof -nP -iTCP:20082 -sTCP:LISTEN` shows which
  process holds it. Only one llama server can use port 20082 at a time.
- **The Mac node is not `Ready`:** the node adapter logs to
  `~/Library/Application Support/macnative/kube/livekit/mackube.log`.

The [voice cluster guide](VOICE_CLUSTER.md) explains each part in more detail.
