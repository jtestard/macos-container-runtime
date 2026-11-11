# Local voice cluster

The dedicated Kind cluster named `livekit` runs the LiveKit SFU and browser web
UI as Linux/arm64 Pods. Four `darwin/arm64` images run as native macOS
processes through `macd` and Virtual Kubelet: the Metal LLM, Whisper STT,
Kokoro TTS, and LiveKit voice agent. One `macd` holds the four native images,
and one `mackube` registers the Mac as a single virtual node for all four Pods.

The cluster and its `mackube` are created and removed together by
`scripts/kube-up.sh livekit` and `scripts/kube-down.sh livekit`, described in
[Kubernetes usage](KUBERNETES.md). Every command below names the
`kind-livekit` kubectl context explicitly.

## Progress

- [x] Build LiveKit and web UI Deployments for Kind, including host mappings for signaling, UDP media, and the browser.
- [x] Build the LLM, Whisper, Kokoro, and agent as `darwin/arm64` OCI images.
- [x] Schedule the four native images on one macOS virtual node.
- [x] Verify the image-built Whisper and Kokoro speech round trip.
- [x] Verify an image-built agent joins a LiveKit room and synthesizes its greeting through image-built Kokoro.
- [x] Publish a spoken question through LiveKit and verify the agent reaches Whisper, the Metal LLM, and Kokoro.
- [x] Replace the LLM Pod with the model-free llama-server image and a read-only hostPath GGUF; verify health, models, and chat responses on 8080.
- [x] Verify the voice agent against the model-free llama-server Pod with the host-mounted GGUF: a spoken question was transcribed and answered aloud by Mistral 7B.
- [ ] Verify a spoken browser turn using a microphone.

## Components

| Service | Placement | Host endpoint |
| --- | --- | --- |
| LiveKit SFU | Linux Pod in Kind | 7880/TCP, 7881/TCP, 7882/UDP |
| Web UI | Linux Pod in Kind | 8090/TCP |
| `llama-server` LLM | native `macnative` Pod | 8080/TCP |
| Whisper STT | native `macnative` Pod | 8000/TCP |
| Kokoro TTS | native `macnative` Pod | 8880/TCP |
| Voice agent | native `macnative` Pod | 8083/TCP |

Kind's port mapping is fixed when its node container is created. The
`kind-cluster.yaml` file maps 7880/TCP for signaling, 7881/TCP and 7882/UDP
for LiveKit media, and 8090/TCP for the web UI. `kubectl port-forward` cannot
carry the UDP media stream. The web page advertises
`ws://localhost:7880` to the browser. The native agent reaches the other
native services on their local host ports; Kubernetes Service routing to native
Pods is not implemented yet.

## Images

Nothing is built here. All five images are published on Docker Hub from the
[macos-native-images](https://github.com/jtestard/macos-native-images)
repository, which holds their build recipes and the voice app's source:

| Image | Built from |
| --- | --- |
| `jtstormz/llama-server:ee0445c` | upstream llama.cpp; model mounted from the Mac, see [LLAMA_SERVER_IMAGE.md](LLAMA_SERVER_IMAGE.md) |
| `jtstormz/whisper-server:v1.9.1-base.en` | upstream whisper.cpp with the English base model |
| `jtstormz/kokoro:b4ef64b` | upstream Kokoro-FastAPI with the Kokoro-82M weights |
| `jtstormz/voice-agent:0.1.0` | the demo agent in that repository's `voice/agent` |
| `jtstormz/voice-web:0.1.0` (Linux) | the demo browser client in its `voice/web` |

## Start

Build `imgrun` and `macd`:

```sh
go build -o .build/imgrun ./cmd/imgrun
go build -o .build/macd ./cmd/macd
```

Start one `macd` for the voice stack on its own socket and image store. Keep
it running in a terminal or under a process supervisor:

```sh
.build/macd -runner .build/imgrun -store .build/voice-images \
  -socket /private/tmp/macnative-voice-docker.sock
```

In another terminal, pull the four native images into it:

```sh
for image in jtstormz/llama-server:ee0445c jtstormz/whisper-server:v1.9.1-base.en \
    jtstormz/kokoro:b4ef64b jtstormz/voice-agent:0.1.0; do
  docker -H unix:///private/tmp/macnative-voice-docker.sock pull "$image"
done
```

Pulled images stay in `.build/voice-images` across `macd` restarts. Restarting
this `macd` stops the native processes; delete the native Pods afterwards so
Kubernetes recreates them on the new `macd`.

Once the socket exists, create the dedicated cluster with its port mappings
and join the Mac to it as the node `macnative`, served by this `macd`:

```sh
KIND_CONFIG=examples/voice-cluster/livekit/kind-cluster.yaml \
MACD_SOCKET=/private/tmp/macnative-voice-docker.sock \
  ./scripts/kube-up.sh livekit
```

If another cluster's `mackube` already uses port 10250, also set
`MACKUBE_PORT` to a free port. Then deploy the Linux Pods; Kind pulls LiveKit
and the web client from Docker Hub:

```sh
kubectl --context kind-livekit apply -k examples/voice-cluster/livekit
kubectl --context kind-livekit apply -f examples/voice-cluster/web.yaml
kubectl --context kind-livekit -n livekit rollout status deployment/livekit
kubectl --context kind-livekit -n livekit rollout status deployment/voice-web
```

The LLM Deployment uses the model-free `llama-server` image with
`Mistral-7B-Instruct-v0.3-Q4_K_M.gguf` as its default model. The commands below
download it (about 4.4 GB) from Hugging Face into `~/models/mistral-7b`;
`MODEL_DIR` selects the model directory. The manifest's `__MODEL_DIR__` placeholder is filled when you
apply it, so no personal path is stored in the YAML. **Apply only the rendered
manifest below.** Applying the template file directly leaves `__MODEL_DIR__`
in the Pod, and `mackube` rejects that relative path. If this already happened,
the rendered `kubectl apply` command below updates the Deployment and replaces
the rejected Pod. If you choose another GGUF, change the `-m` argument in the
manifest to match its file name. The image defaults to port 8082, so the Pod
adds `--port 8080`, where the voice agent expects the LLM.
On the tested 16 GB Mac, loading this
GGUF with the image's default mmap mode stalled in Metal residency. The Pod
sets `--load-mode none` and offloads 16 layers to Metal; that configuration
reached `/health` successfully. `llama-server` logs duplicate-argument warnings
for the port and GPU layer count because the Pod overrides image defaults; the
appended values win. Deploy the three API services first. Their Pods
report `Running` when the process starts; the LLM and Kokoro need more time
to load models and Metal kernels. Check their HTTP endpoints before starting
the agent:

```sh
MODEL_DIR="$HOME/models/mistral-7b"
mkdir -p "$MODEL_DIR"
test -f "$MODEL_DIR/Mistral-7B-Instruct-v0.3-Q4_K_M.gguf" || curl -fL \
  -o "$MODEL_DIR/Mistral-7B-Instruct-v0.3-Q4_K_M.gguf" \
  https://huggingface.co/bartowski/Mistral-7B-Instruct-v0.3-GGUF/resolve/main/Mistral-7B-Instruct-v0.3-Q4_K_M.gguf
sed "s|__MODEL_DIR__|$MODEL_DIR|g" examples/voice-cluster/llama-server.yaml \
  | kubectl --context kind-livekit apply -f -
kubectl --context kind-livekit apply \
  -f examples/voice-cluster/whisper.yaml \
  -f examples/voice-cluster/kokoro.yaml
curl http://127.0.0.1:8080/health
curl http://127.0.0.1:8880/health
kubectl --context kind-livekit apply -f examples/voice-cluster/agent.yaml
kubectl --context kind-livekit -n livekit get pods -o wide
```

Open `http://127.0.0.1:8090` and select **Start conversation**. The web server
mints a room token, the agent joins and greets you, and both sides' words
appear on the page as they are transcribed.

To check the whole loop without a microphone, run the conversation test from a
checkout of macos-native-images. It joins a room, speaks a WAV file, and waits
for the agent's spoken reply:

```sh
cd /path/to/macos-native-images/voice/agent
npm ci   # Node 22
say -o /tmp/question.aiff "What is the capital of France?"
afconvert -f WAVE -d LEI16@48000 -c 1 /tmp/question.aiff /tmp/question.wav
node test-conversation.js /tmp/question.wav
```

The verified run printed:

```text
Agent: Hi! I am running entirely on this Mac. What would you like to talk about?
You  : What is the capital of France?
Agent: The capital of France is Paris.
Conversation OK
```

A browser microphone test still needs a person.
LiveKit logs use `kubectl logs deployment/livekit`. Native Pod logs also work
through Kubernetes, including `--tail` and `-f`:

```sh
kubectl --context kind-livekit -n livekit logs deployment/llama-server --tail=20
stern --context kind-livekit -n livekit kokoro
```

See [Kubernetes usage](KUBERNETES.md#current-limits) for unsupported log
options.

## Stop or recreate

To remove everything, run the script, then stop `macd`:

```sh
./scripts/kube-down.sh livekit
```

It stops the four native Pods (and their macOS process groups) before
stopping `mackube` and deleting the cluster, which also removes the Linux Pods.

To stop only the workloads and keep the cluster, delete the native
Deployments, then the Linux ones:

```sh
kubectl --context kind-livekit delete \
  -f examples/voice-cluster/agent.yaml \
  -f examples/voice-cluster/kokoro.yaml \
  -f examples/voice-cluster/whisper.yaml
kubectl --context kind-livekit -n livekit delete deployment llama-server
kubectl --context kind-livekit -n livekit wait --for=delete pod \
  -l 'app in (llama-server,whisper-stt,kokoro-tts,voice-agent)' --timeout=120s
kubectl --context kind-livekit delete -f examples/voice-cluster/web.yaml
kubectl --context kind-livekit delete -k examples/voice-cluster/livekit
```

Changing `kind-cluster.yaml` port mappings requires recreating the cluster:
run `kube-down.sh livekit`, then follow the start sequence above.

## Prototype limits

`mackube` cannot assign a Pod IP, route a Kubernetes Service to a native Pod,
or inject Pod environment variables. It supports one read-only hostPath
directory per Pod; other volume types are unsupported. The native Pods share the
Mac's host network, and each must listen on a distinct port. Resource capacity
is reported by one virtual node, with a four-Pod limit.
Pod readiness currently means the process is alive, so use the HTTP checks
above for model readiness. The runner uses macOS Seatbelt to limit ordinary
file writes. Kokoro's Metal libraries warn when they cannot write their usual
host cache paths, but speech generation passed. The LiveKit agent's optional
child-memory sampler calls `/bin/ps`, which Seatbelt denies; it logs repeated
`spawn EPERM` warnings while jobs continue to run. Run only trusted images.
