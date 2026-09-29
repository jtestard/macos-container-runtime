# Kind to native macOS Pod prototype

`kube/cmd/mackube` is a Virtual Kubelet provider running on the Mac. It
registers a `macnative` node in the local Kind cluster. A Deployment with the
matching node selector can then create a Pod on that node. The provider asks
the existing `macd` Docker API service to start and supervise the local
`darwin/arm64` image.

The control plane and Kubernetes scheduler remain inside Kind's Linux VM.
The application binary runs as a macOS process on the host. This prototype
uses the host network and supports one Pod with one container at a time.
On this Mac, the example Deployment reached `1/1 Running` on `macnative`,
returned `ok` from port 8081, and its deletion stopped the native process.
The adapter also adopted a running Pod after an adapter restart. The node
changed to NotReady while `macd` was stopped and recovered to Ready when
`macd` restarted.

## Try the Deployment

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

Export a kubeconfig containing only the local Kind cluster. `mackube`
requires exactly one context, named `kind-kind` by default; pass
`-kind-cluster <name>` for a different dedicated Kind cluster. It refuses
other contexts to avoid changing an unrelated cluster.

```sh
kind export kubeconfig --name kind --kubeconfig .build/kind-kubeconfig
cd kube
go build -o ../.build/mackube ./cmd/mackube
../.build/mackube -kubeconfig ../.build/kind-kubeconfig \
  -socket /private/tmp/macnative-docker.sock
```

The adapter module requires Go 1.26 or newer. If your shell has `GOROOT` set
to an older Go installation, unset it before `go build`. In another terminal,
explicitly target Kind for every Kubernetes command:

```sh
kubectl --context kind-kind get node macnative
kubectl --context kind-kind apply -f examples/kubernetes/tiny-web.yaml
kubectl --context kind-kind get pods -o wide
kubectl --context kind-kind get deployment macnative-tiny-web
curl http://127.0.0.1:8081/healthz
docker --context macnative ps
kubectl --context kind-kind delete -f examples/kubernetes/tiny-web.yaml
```

The Deployment uses a node selector and matching toleration so the Pod can
only be scheduled to the synthetic node. It uses `Recreate` and one replica
because both old and new server processes would otherwise contend for port
8081. Kubernetes does not allow `spec.os.name: darwin`, so the placement
label is our own convention.

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
