# Local voice cluster

The dedicated Kind cluster named `livekit` runs the LiveKit SFU and browser web
UI as Linux/arm64 Pods. Four `darwin/arm64` images run as native macOS
processes through `macd` and Virtual Kubelet: the Metal LLM, Whisper STT,
Kokoro TTS, and LiveKit voice agent. Each native service gets its own Docker
socket and virtual node because this prototype registers one image and runs
one Pod per `macd` and `mackube` pair.

The older `kind-kind` cluster has been removed. The default kubeconfig now
selects `kind-livekit`; every command below also names the dedicated
`.build/livekit-kubeconfig` explicitly.

## Progress

- [x] Build LiveKit and web UI Deployments for Kind, including host mappings for signaling, UDP media, and the browser.
- [x] Build the LLM, Whisper, Kokoro, and agent as `darwin/arm64` OCI images.
- [x] Schedule each native image on its labeled macOS virtual node.
- [x] Verify the image-built Whisper and Kokoro speech round trip.
- [x] Verify an image-built agent joins a LiveKit room and synthesizes its greeting through image-built Kokoro.
- [x] Publish a spoken question through LiveKit and verify the agent reaches Whisper, the Metal LLM, and Kokoro.
- [ ] Verify a spoken browser turn using a microphone.

## Components

| Service | Placement | Host endpoint |
| --- | --- | --- |
| LiveKit SFU | Linux Pod in Kind | 7880/TCP, 7881/TCP, 7882/UDP |
| Web UI | Linux Pod in Kind | 8090/TCP |
| `go-inf-server` LLM | native `macnative` Pod | 8080/TCP |
| Whisper STT | native `macnative-whisper` Pod | 8000/TCP |
| Kokoro TTS | native `macnative-kokoro` Pod | 8880/TCP |
| Voice agent | native `macnative-agent` Pod | 8083/TCP |

Kind's port mapping is fixed when its node container is created. The
`kind-cluster.yaml` file maps 7880/TCP for signaling, 7881/TCP and 7882/UDP
for LiveKit media, and 8090/TCP for the web UI. `kubectl port-forward` cannot
carry the UDP media stream. The web page advertises
`ws://localhost:7880` to the browser. The native agent uses local host
endpoints from the packaged `speech.env`; Kubernetes Service routing to native
Pods is not implemented yet.

## Build and start

Build the images using [VOICE_IMAGES.md](VOICE_IMAGES.md). The LLM image build
is also described in [the example README](../examples/go-inf-server/README.md).
Build `imgrun`, `macd`, and `mackube` as in [KUBERNETES.md](KUBERNETES.md):

```sh
go build -o .build/imgrun ./cmd/imgrun
go build -o .build/macd ./cmd/macd
(cd kube && go build -o ../.build/mackube ./cmd/mackube)
```

Create the dedicated cluster, load the web image, and deploy its Linux Pods:

```sh
kind create cluster --config examples/voice-cluster/livekit/kind-cluster.yaml \
  --kubeconfig .build/livekit-kubeconfig
kind load docker-image macvoice-web:local --name livekit
kubectl --kubeconfig .build/livekit-kubeconfig apply -k examples/voice-cluster/livekit
kubectl --kubeconfig .build/livekit-kubeconfig apply -f examples/voice-cluster/web.yaml
kubectl --kubeconfig .build/livekit-kubeconfig -n livekit rollout status deployment/livekit
kubectl --kubeconfig .build/livekit-kubeconfig -n livekit rollout status deployment/voice-web
```

To make plain `kubectl` use this cluster as well, run
`kind export kubeconfig --name livekit`. This adds `kind-livekit` to the
default kubeconfig and selects it without removing other non-Kind contexts.

Start one `macd` per image. Keep each process running in its own terminal or
under a process supervisor:

```sh
.build/macd -image .build/go-inf-server-smollm2.tar -tag go-inf-server:smollm2 \
  -runner .build/imgrun -socket /private/tmp/macnative-voice-docker.sock
.build/macd -image .build/whisper-base-en.tar -tag whisper-base-en:local \
  -runner .build/imgrun -socket /private/tmp/macnative-whisper-docker.sock
.build/macd -image .build/kokoro-mps.tar -tag kokoro-mps:local \
  -runner .build/imgrun -socket /private/tmp/macnative-kokoro-docker.sock
.build/macd -image .build/voice-agent.tar -tag voice-agent:local \
  -runner .build/imgrun -socket /private/tmp/macnative-agent-docker.sock
```

Once the sockets exist, start one `mackube` per socket. `-slot` labels each
node so the scheduler selects the correct registered image:

```sh
.build/mackube -kind-cluster livekit -kubeconfig .build/livekit-kubeconfig \
  -node-name macnative -slot llm -socket /private/tmp/macnative-voice-docker.sock
.build/mackube -kind-cluster livekit -kubeconfig .build/livekit-kubeconfig \
  -node-name macnative-whisper -slot whisper -socket /private/tmp/macnative-whisper-docker.sock
.build/mackube -kind-cluster livekit -kubeconfig .build/livekit-kubeconfig \
  -node-name macnative-kokoro -slot kokoro -socket /private/tmp/macnative-kokoro-docker.sock
.build/mackube -kind-cluster livekit -kubeconfig .build/livekit-kubeconfig \
  -node-name macnative-agent -slot agent -socket /private/tmp/macnative-agent-docker.sock
```

Deploy the three API services first. Their Pods report `Running` when the
process starts; the LLM and Kokoro need more time to load models and Metal
kernels. Check their HTTP endpoints before starting the agent:

```sh
kubectl --kubeconfig .build/livekit-kubeconfig apply \
  -f examples/voice-cluster/go-inf-server.yaml \
  -f examples/voice-cluster/whisper.yaml \
  -f examples/voice-cluster/kokoro.yaml
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8880/health
kubectl --kubeconfig .build/livekit-kubeconfig apply -f examples/voice-cluster/agent.yaml
kubectl --kubeconfig .build/livekit-kubeconfig -n livekit get pods -o wide
```

Open `http://127.0.0.1:8090`. The web server mints a room token and points
the browser at the local LiveKit signaling address. Test API and speech paths:

```sh
curl http://127.0.0.1:8090/config
/absolute/path/to/go-inf-server/scripts/test-speech.sh
```

The speech script transcribes a known sample, synthesizes PCM, and feeds that
audio back to Whisper. An automated LiveKit room test published Opus speech,
which Whisper transcribed; the agent then generated and synthesized a reply.
The current small SmolLM2 model answered that arithmetic question incorrectly,
so this verifies transport and service integration rather than answer quality.
A browser microphone test still needs a person.
LiveKit logs use `kubectl logs deployment/livekit`. Native Pod log streaming
through Kubernetes is not implemented; use `docker -H unix://<socket> ps` to
find the container ID, then `docker -H unix://<socket> logs <id>`.

## Stop or recreate

Delete native Deployments before stopping their adapters so `mackube` can
terminate the macOS process groups. Then stop the four `mackube` processes and
the four `macd` processes. To stop the Linux Pods as well, delete the web
manifest and LiveKit kustomization:

```sh
kubectl --kubeconfig .build/livekit-kubeconfig delete \
  -f examples/voice-cluster/agent.yaml \
  -f examples/voice-cluster/kokoro.yaml \
  -f examples/voice-cluster/whisper.yaml \
  -f examples/voice-cluster/go-inf-server.yaml
kubectl --kubeconfig .build/livekit-kubeconfig -n livekit wait --for=delete pod \
  -l 'app in (go-inf-server,whisper-stt,kokoro-tts,voice-agent)' --timeout=120s
kubectl --kubeconfig .build/livekit-kubeconfig delete -f examples/voice-cluster/web.yaml
kubectl --kubeconfig .build/livekit-kubeconfig delete -k examples/voice-cluster/livekit
```

Changing `kind-cluster.yaml` port mappings requires recreating this dedicated
cluster. After stopping native Pods and adapters, use
`kind delete cluster --name livekit --kubeconfig .build/livekit-kubeconfig`,
then follow the start sequence above.

## Prototype limits

`mackube` cannot assign a Pod IP, route a Kubernetes Service to a native Pod,
inject Pod environment variables, or mount volumes. The native Pods share the
Mac's host network, and each must listen on a distinct port. Resource capacity
is reported separately for each virtual node even though they share one Mac.
Pod readiness currently means the process is alive, so use the HTTP checks
above for model readiness. The runner uses macOS Seatbelt to limit ordinary
file writes. Kokoro's Metal libraries warn when they cannot write their usual
host cache paths, but speech generation passed. The LiveKit agent's optional
child-memory sampler calls `/bin/ps`, which Seatbelt denies; it logs repeated
`spawn EPERM` warnings while jobs continue to run. Run only trusted images.
