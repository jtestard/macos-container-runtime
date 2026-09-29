# Kubernetes usage

`kube/cmd/mackube` is a Virtual Kubelet provider running on the Mac. It
registers a native node in a Kubernetes cluster. A Deployment with the
matching node selector can then create a Pod on that node. The provider asks
the existing `macd` Docker API service to start and supervise the local
`darwin/arm64` image.

The Kubernetes distribution is not part of the runtime contract. `mackube`
connects to the API server selected by an explicit, single-context kubeconfig;
the Mac needs API access and credentials that can manage the virtual node and
its Pods. Kind is the locally tested example below. Other distributions have
not been validated yet and may need their own RBAC or admission configuration.
The application binary runs as a macOS process on the host. This prototype
uses the host network and supports one Pod with one container at a time.
Multiple `macd` sockets can each serve one image, with one `mackube` process
per socket. Give each adapter a distinct `-node-name` and `-slot` so a
Deployment selects the node that has its image; the
[voice cluster](VOICE_CLUSTER.md) uses this pattern for four native services.

## Run a native Pod

These commands use the existing Kind cluster named `livekit` and keep the
example on its own virtual node, `macnative-demo`. They do not change your
default Kubernetes context. If the cluster does not exist, create it first as
described in the [voice cluster guide](VOICE_CLUSTER.md). Run commands from the
repository root. You need Docker Desktop, Kind, `kubectl`, the Docker CLI with
Buildx, and Go 1.26 or later for `mackube`.

Build the small Kubernetes test image through the same Buildx remote worker
used by [the original tiny web example](../examples/tiny-web/README.md). The
test image listens on port 8081 so it can run alongside a Metal server using
port 8080. Start the BuildKit worker first, as shown in the main README.

```sh
docker buildx build --builder macnative --platform darwin/arm64 \
  --build-context "go-toolchain=$(go env GOROOT)" \
  --progress plain \
  --output type=oci,dest=.build/kube-web.tar \
  examples/kubernetes/web
```

Then start `macd` with that image in one terminal:

```sh
go build -o .build/imgrun ./cmd/imgrun
go build -o .build/macd ./cmd/macd
.build/macd -image .build/kube-web.tar -tag kube-web:latest \
  -runner .build/imgrun -socket /private/tmp/macnative-docker.sock
```

Only one `macd` may own that socket. Stop the existing instance first if it
currently serves another image. The test server uses host port 8081.

Export a kubeconfig containing only the `livekit` Kind cluster. `mackube`
requires exactly one selected context, but does not require a Kind context.
The optional `-kind-cluster livekit` flag retains the older Kind-specific
context check if you want it.

```sh
kind export kubeconfig --name livekit --kubeconfig .build/livekit-kubeconfig
cd kube
go build -o ../.build/mackube ./cmd/mackube
../.build/mackube -kubeconfig ../.build/livekit-kubeconfig \
  -node-name macnative-demo -slot demo \
  -socket /private/tmp/macnative-docker.sock
```

The adapter module requires Go 1.26 or newer. If your shell has `GOROOT` set
to an older Go installation, unset it before `go build`. In another terminal,
use the dedicated kubeconfig for every Kubernetes command (from the
repository root):

```sh
kubectl --kubeconfig .build/livekit-kubeconfig get node macnative-demo
kubectl --kubeconfig .build/livekit-kubeconfig apply -f examples/kubernetes/tiny-web.yaml
kubectl --kubeconfig .build/livekit-kubeconfig get pods -o wide
kubectl --kubeconfig .build/livekit-kubeconfig get deployment macnative-tiny-web
curl http://127.0.0.1:8081/healthz
docker --context macnative ps
kubectl --kubeconfig .build/livekit-kubeconfig delete -f examples/kubernetes/tiny-web.yaml
```

The Deployment uses runtime and slot selectors with a matching toleration, so
the Pod can only be scheduled to `macnative-demo`. It uses `Recreate` and one
replica because both old and new server processes would otherwise contend for
port 8081. Kubernetes does not allow `spec.os.name: darwin`, so the placement
label is our own convention.

For another Kubernetes distribution, provide a dedicated kubeconfig with its
single intended context selected, then use that path with `mackube -kubeconfig`
and `kubectl --kubeconfig`. The image, node label, Pod constraints, and host
network behavior below are the same. No Kind VM is needed for the native
process; the API server and scheduler can be elsewhere.

## Current contract

The image must already be registered in `macd` under the exact tag in the
Pod spec. There is no image pull. The Pod must request one ordinary container
with its image's default command, `hostNetwork: true`,
`automountServiceAccountToken: false`, and `enableServiceLinks: false`.
Volumes, image secrets, environment
overrides, probes, exec, port forwarding, metrics, and Kubernetes log
streaming are not implemented. The provider rejects unsupported fields rather
than silently ignoring them.

The provider reports the process as running after `macd` confirms it started;
`Ready` currently means that the process is alive, not that the application
has finished initializing. A process exit becomes a terminal Pod status.
The runtime uses host networking, but it does not give the process a
Kubernetes Pod IP or make a selector based Service route to it. Service
routing and kubelet API operations are separate milestones.

Both `macd` and the provider hold their state in memory. The provider can
adopt a still-running Pod from `macd` after the provider restarts. Restarting
`macd` itself stops all native processes and clears its records. Deleting the
Deployment removes its Pod and stops its native process. Stop the adapter
after deleting the Deployment; the registered node will eventually become
NotReady without heartbeats.
