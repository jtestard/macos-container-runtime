# Kubernetes usage

`mackube` registers a macOS virtual node with an existing Kubernetes cluster.
The example below runs one llama server on that node. The process runs on the
Mac, with access to Metal and a model directory on the Mac. It does not need
the voice stack or a BuildKit build.

Any Kubernetes distribution can provide the control plane, provided this Mac
can reach its API server, the control plane can reach the Mac's virtual kubelet
HTTPS endpoint, and the selected credentials can manage nodes and Pods. Kind
is one tested option. Run these commands from the repository root.
You need a running `macd` (see the [installation guide](../README.md)),
`docker`, `kubectl`, and Go 1.26 or later.

## Run the llama server

1. Pull the published `darwin/arm64` image into `macd`:

   ```sh
   docker context use macnative
   docker pull jtstormz/tiny-web:llama-server-001
   ```

2. Save your **current** Kubernetes context as a private, single-context
   kubeconfig and build the adapter:

   ```sh
   mkdir -p .build
   umask 077
   kubectl config current-context
   kubectl config view --minify --flatten --raw > .build/macnative-kubeconfig
   go -C kube build -o ../.build/mackube ./cmd/mackube
   ```

   Confirm the displayed context is the cluster you intend to use. The adapter
   uses only the saved file, so switching your regular Kubernetes context later
   will not change its target cluster.

3. Start one virtual node in a separate terminal:

   ```sh
   .build/mackube -kubeconfig .build/macnative-kubeconfig \
     -node-name macnative-llama -slot llama \
     -socket /private/tmp/macnative-docker.sock \
     -kubelet-address host.docker.internal -kubelet-port 10250
   ```

   For Kind on Docker Desktop, `host.docker.internal` resolves inside the
   control-plane container to the Mac. Use a different reachable address for
   other clusters and a distinct port for each virtual node. `mackube` serves
   HTTPS on that port and requires a client certificate signed by the cluster
   CA in the saved kubeconfig.

4. In the original terminal, set `MODEL_DIR` to the **absolute path of the
   directory** containing `SmolLM2-360M-Instruct-Q8_0.gguf` on this Mac. The
   file name in the manifest must match exactly. Render the placeholder and
   create the Deployment:

   ```sh
   MODEL_DIR="/absolute/path/to/model-directory"
   test -f "$MODEL_DIR/SmolLM2-360M-Instruct-Q8_0.gguf"
   sed "s|__MODEL_DIR__|$MODEL_DIR|g" examples/kubernetes/llama-server.yaml \
     | kubectl --kubeconfig .build/macnative-kubeconfig apply -f -
   kubectl --kubeconfig .build/macnative-kubeconfig get pods \
     -n default -l app=macnative-llama -o wide
   ```

   The template uses `__MODEL_DIR__` so no personal model path is stored in
   the repository. Set `MODEL_DIR` to your real directory before applying it.
   The image expects the model at `/app/models` and listens on host port 8082.

5. Check the server and remove the example when finished:

   ```sh
   curl http://127.0.0.1:8082/health
   kubectl --kubeconfig .build/macnative-kubeconfig logs -n default deployment/macnative-llama --tail=20
   docker ps
   kubectl --kubeconfig .build/macnative-kubeconfig delete deployment macnative-llama -n default
   ```

   Stop `mackube` with Ctrl-C after deleting the Deployment. The node will
   eventually become NotReady without the adapter's heartbeats.

## Current limits

The image must already be present in `macd`; `mackube` does not pull it. The
adapter supports up to four Pods with one container each. The example uses a
single replica, `Recreate`, a node selector, and a matching toleration to place
the Pod on the `macnative-llama` node. Port 8082 must be free on the Mac. The
Pod uses host networking, so it has no separate Pod IP and a selector-based
Kubernetes Service cannot route to it.

The Pod has the image's default entrypoint, no environment overrides, and
one read-only `hostPath` directory mounted at an image path that does not
already exist. The directory is read from the Mac running `macd`. Image pull
secrets, probes, exec, port forwarding, and metrics are not supported. Native
Pod logs support `kubectl logs`, `--tail`, `-f`, and `--tail` with `-f` as used
by Stern. The `--previous`, `--since`, `--timestamps`, and `--limit-bytes`
options are not yet supported. Log history is held in memory, up to 1 MiB per
container; tail selection uses that retained history.

Kubernetes `Ready` currently means the process is alive; the model can still
be loading. Check `/health` before sending inference requests. Both `macd`
and `mackube` keep state in memory. Restarting `macd` stops native processes;
the adapter can adopt a still-running Pod after it restarts.

For the larger LiveKit setup, see the [voice cluster guide](VOICE_CLUSTER.md).
