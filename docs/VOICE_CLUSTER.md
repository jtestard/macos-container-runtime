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
- [ ] Verify the voice agent against the model-free llama-server Pod with the host-mounted GGUF.
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
`ws://localhost:7880` to the browser. The native agent uses local host
endpoints from the packaged `speech.env`; Kubernetes Service routing to native
Pods is not implemented yet.

## Build and start

Build the images using [VOICE_IMAGES.md](VOICE_IMAGES.md). The LLM image build
and model mount are described in [LLAMA_SERVER_IMAGE.md](LLAMA_SERVER_IMAGE.md).
Build `imgrun` and `macd`:

```sh
go build -o .build/imgrun ./cmd/imgrun
go build -o .build/macd ./cmd/macd
```

Start one `macd` with all four local OCI images, on its own socket. Keep it
running in a terminal or under a process supervisor:

```sh
.build/macd \
  -load jtstormz/tiny-web:llama-server-001=.build/llama-server.tar \
  -load whisper-base-en:local=.build/whisper-base-en.tar \
  -load kokoro-mps:local=.build/kokoro-mps.tar \
  -load voice-agent:local=.build/voice-agent.tar \
  -runner .build/imgrun -socket /private/tmp/macnative-voice-docker.sock
```

Once the socket exists, create the dedicated cluster with its port mappings
and join the Mac to it as the node `macnative`, served by this `macd`:

```sh
KIND_CONFIG=examples/voice-cluster/livekit/kind-cluster.yaml \
MACD_SOCKET=/private/tmp/macnative-voice-docker.sock \
  ./scripts/kube-up.sh livekit
```

If another cluster's `mackube` already uses port 10250, also set
`MACKUBE_PORT` to a free port. Then load the web image and deploy the Linux
Pods:

```sh
DOCKER_CONTEXT=desktop-linux kind load docker-image macvoice-web:local --name livekit
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
adds `--port 8080` to keep the voice agent's packaged `LLM_BASE_URL` valid.
On the tested 16 GB Mac, loading this
GGUF with the image's default mmap mode stalled in Metal residency. The Pod
sets `--load-mode none` and offloads 16 layers to Metal; that configuration
reached `/health` successfully. `llama-server` logs duplicate-argument warnings
for the port and GPU layer count because the Pod overrides image defaults; the
appended values win. If `.build/llama-server.tar` is absent,
download the published tag as an OCI tarball using the
[root README](../README.md#run-a-llama-server-with-metal)
before starting `macd`. Deploy the three API services first. Their Pods
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

Open `http://127.0.0.1:8090`. The web server mints a room token and points
the browser at the local LiveKit signaling address. Test API and speech paths:

```sh
curl http://127.0.0.1:8090/config
"$APP_SOURCE/scripts/test-speech.sh"
```

The speech script transcribes a known sample, synthesizes PCM, and feeds that
audio back to Whisper. An automated LiveKit room test published Opus speech,
which Whisper transcribed; the agent then generated and synthesized a reply.
That earlier test used SmolLM2, which answered the arithmetic question
incorrectly. It verified transport and service integration, not the new
Mistral default or answer quality.
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
