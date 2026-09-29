# Local voice cluster

This setup uses a dedicated Kind cluster named `livekit` and the `macnative`
virtual node. The LiveKit SFU runs as a Linux Pod. `go-inf-server` runs from a
`darwin/arm64` OCI image as a native macOS process managed by a Kubernetes
Deployment. Kind's node container publishes 7880/TCP, 7881/TCP, and 7882/UDP
on macOS loopback; all three mappings are needed for browser audio.

The current virtual node supports one native Pod and one OCI image per `macd`
instance. It also has no Pod IP, Service routing, environment injection, or
volume support. Whisper, Kokoro, the LiveKit agent, and the browser web app
therefore use the existing native process manager in `go-inf-server` until
those runtime capabilities exist. This is a functioning six-service local
voice loop, with Kubernetes managing the SFU and LLM.

## Build plan

- [x] Inspect the six services, existing images, ports, and previous Kind LiveKit configuration.
- [x] Keep the existing `kind` cluster and create a dedicated `livekit` cluster with media ports mapped.
- [x] Allow `mackube` to select an explicitly named Kind cluster.
- [x] Apply the LiveKit Deployment and verify signaling and UDP media.
- [x] Run the existing Metal LLM image through `macd` and `mackube` and verify a Kubernetes Deployment.
- [x] Start native STT, TTS, agent, and web services.
- [x] Verify LiveKit dispatches the agent and Kokoro synthesizes its greeting.
- [ ] Verify a spoken browser turn through STT, LLM, and TTS with a microphone.

## Components

| Component | Placement | Host port | Access |
| --- | --- | --- | --- |
| LiveKit SFU | Linux Pod in Kind | 7880/TCP, 7881/TCP, 7882/UDP | Browser and agent |
| go-inf-server | macOS virtual Pod | 8080/TCP | Agent |
| Whisper STT | macOS process | 8000/TCP | Agent |
| Kokoro TTS | macOS process | 8880/TCP | Agent |
| Voice agent | macOS process | 8083/TCP | LiveKit and all three model APIs |
| Web UI | macOS process | 8090/TCP | Browser |

The copied LiveKit manifests in `examples/voice-cluster/livekit` come from
`go-inf-server` commit `29e142b`, which documented a previously working Kind
media path. Its `kind-cluster.yaml` fixes the port mappings at cluster
creation. `kubectl port-forward` cannot carry the UDP media stream.

On this Mac, both Deployments reached `1/1`; `/healthz` responded from the
Metal LLM and web server; the agent registered with LiveKit and joined a
test room; LiveKit selected UDP media on `127.0.0.1:7882`; and Kokoro
synthesized the agent's greeting. The existing `test-speech.sh` also passed
the Whisper transcription, Kokoro PCM response, and TTS-to-STT round trip.
A browser microphone turn still needs a human test.

## Operations

All Kubernetes commands must use `.build/livekit-kubeconfig` explicitly.
The separate kubeconfig protects other contexts from accidental changes.

```sh
kind create cluster --config examples/voice-cluster/livekit/kind-cluster.yaml \
  --kubeconfig .build/livekit-kubeconfig
kubectl --kubeconfig .build/livekit-kubeconfig apply -k examples/voice-cluster/livekit
kubectl --kubeconfig .build/livekit-kubeconfig -n livekit rollout status deployment/livekit
```

Build `imgrun`, `macd`, and `mackube` as described in
[KUBERNETES.md](KUBERNETES.md). The LLM image build is in
[the example README](../examples/go-inf-server/README.md). Start these two
long-running processes in separate terminals:

```sh
.build/macd -image .build/go-inf-server-smollm2.tar \
  -tag go-inf-server:smollm2 -runner .build/imgrun \
  -socket /private/tmp/macnative-voice-docker.sock
.build/mackube -kind-cluster livekit \
  -kubeconfig .build/livekit-kubeconfig \
  -socket /private/tmp/macnative-voice-docker.sock
```

Then deploy the native image:

```sh
kubectl --kubeconfig .build/livekit-kubeconfig apply -f examples/voice-cluster/go-inf-server.yaml
kubectl --kubeconfig .build/livekit-kubeconfig -n livekit rollout status deployment/go-inf-server
curl http://127.0.0.1:8080/healthz
```

From the `go-inf-server` checkout, start the remaining native services:

```sh
./scripts/services.sh start stt tts web agent
./scripts/services.sh status
```

Open `http://127.0.0.1:8090`. The web page uses
`ws://localhost:7880` and the agent uses the local `speech.env` endpoints.
Kokoro takes time to load its MPS model before port 8880 opens. The virtual
node currently marks the LLM Pod Ready as soon as its process starts, before
the GGUF and Metal kernels finish loading; check `/healthz` as well as the
Deployment status before starting the agent.
To inspect the SFU, use
`kubectl --kubeconfig .build/livekit-kubeconfig -n livekit logs deployment/livekit`.
The native Pod's logs are available through its dedicated `macd` Docker
socket; Kubernetes log streaming is not implemented yet.

To stop the voice workload, stop the agent, web, TTS, and STT processes with
`./scripts/services.sh stop agent web tts stt`, delete the LLM Deployment,
then stop `mackube` and `macd`. Delete the LiveKit workload with
`kubectl --kubeconfig .build/livekit-kubeconfig delete -k examples/voice-cluster/livekit`.
Deleting the dedicated cluster is `kind delete cluster --name livekit`.
