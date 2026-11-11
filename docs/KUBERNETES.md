# Kubernetes usage

`mackube` joins this Mac to a Kubernetes cluster as a virtual node. Pods
scheduled on that node run as native macOS processes through `macd`, with
access to Metal and to directories on the Mac. The example below runs one
llama server this way. It does not need the voice stack or a BuildKit build.

The cluster is a local [Kind](https://kind.sigs.k8s.io/) cluster in Docker
Desktop, and `mackube` lives exactly as long as that cluster: two scripts
create and remove them together. Run the commands from the repository root.

You need a running `macd` (see the [installation guide](../README.md)),
Docker Desktop, `kind`, `kubectl`, and Go 1.26 or later.

## Start and stop the cluster

```sh
./scripts/kube-up.sh      # create cluster "macnative" and its Mac node
./scripts/kube-down.sh    # remove both
```

`kube-up.sh` creates the Kind cluster in Docker Desktop (whatever your current
Docker context is), saves its kubeconfig, builds `mackube`, starts it in the
background, and waits until the Mac node `macnative` is `Ready`. It also
selects the `kind-macnative` kubectl context.

`kube-down.sh` stops the native Pods on the Mac node, stops `mackube`, deletes
the Kind cluster, and deletes the cluster's local files. No node, process,
or kubectl context is left behind.

| Situation | What happens |
| --- | --- |
| `mackube` crashes | launchd restarts it; running Pods keep running and are adopted. |
| You run `kube-up.sh` again | Safe. It restarts `mackube`, which adopts running Pods instead of starting them twice. |
| You log out or reboot | Nothing starts by itself. Run `kube-up.sh` again; it reuses the existing cluster. |
| `macd` restarts | Native processes stop; `kube-up.sh` is not needed, `mackube` reconnects. |

To check on it:

```sh
kubectl --context kind-macnative get node macnative
pgrep -lx mackube
tail "$HOME/Library/Application Support/macnative/kube/macnative/mackube.log"
```

The cluster's kubeconfig, `mackube` launchd job, and log live in
`~/Library/Application Support/macnative/kube/<cluster>/`. The launchd job is
named `dev.macnative.mackube.<cluster>`; its plist stays in that directory,
not in `~/Library/LaunchAgents`, which is why it never starts at login.

## Run the llama server

1. Pull the published `darwin/arm64` image into `macd` and start the cluster:

   ```sh
   docker --context macnative pull jtstormz/llama-server:ee0445c-2
   ./scripts/kube-up.sh
   ```

2. Set `MODEL_DIR` to the **absolute path of the directory** containing
   `SmolLM2-360M-Instruct-Q8_0.gguf` on this Mac. The file name in the manifest
   must match exactly. Render the placeholder and create the Deployment:

   ```sh
   MODEL_DIR="/absolute/path/to/model-directory"
   test -f "$MODEL_DIR/SmolLM2-360M-Instruct-Q8_0.gguf"
   sed "s|__MODEL_DIR__|$MODEL_DIR|g" examples/kubernetes/llama-server.yaml \
     | kubectl --context kind-macnative apply -f -
   kubectl --context kind-macnative get pods -l app=macnative-llama -o wide
   ```

   The template uses `__MODEL_DIR__` so no personal model path is stored in
   the repository. The image expects the model at `/app/models` and listens on
   host port 20082, which must be free.

3. Check the server:

   ```sh
   curl http://127.0.0.1:20082/health
   kubectl --context kind-macnative logs deployment/macnative-llama --tail=20
   docker --context macnative ps
   ```

4. Remove the example, or the whole cluster:

   ```sh
   kubectl --context kind-macnative delete deployment macnative-llama
   ./scripts/kube-down.sh
   ```

   Deleting the Deployment stops the native process. `kube-down.sh` alone is
   also enough: it stops any native Pods before deleting the cluster.

## Options

`kube-up.sh` and `kube-down.sh` take an optional cluster name (default
`macnative`), so several clusters can coexist. `kube-up.sh` also reads these
environment variables:

| Variable | Default | Use |
| --- | --- | --- |
| `KIND_CONFIG` | none | Kind cluster config file, for port mappings and similar. |
| `MACKUBE_NODE` | `macnative` | Name of the Mac node. |
| `MACKUBE_SLOT` | none | Adds a `macnative.dev/slot` node label for Pod placement. |
| `MACKUBE_PORT` | `20250` | Virtual kubelet HTTPS port; each running cluster needs its own. |
| `MACD_SOCKET` | `/private/tmp/macnative-docker.sock` | The `macd` instance that runs this cluster's Pods. |
| `KIND_DOCKER_CONTEXT` | `desktop-linux` | Docker context that runs Kind. |

The scripts always run Kind against Docker Desktop. If you made `macnative`
your default Docker context, running `kind` by hand does not work: Kind follows
the current context, so `kind get clusters` silently lists nothing and
`kind create cluster` fails because `macd` cannot run Linux images. Prefix
such commands with `DOCKER_CONTEXT=desktop-linux`.

`mackube` itself can join any cluster whose API server the Mac can reach and
which can reach the Mac's virtual kubelet port; run
`$HOME/Library/Application\ Support/macnative/bin/mackube -h` for its flags.
The scripts cover only Kind.

## Current limits

The image must already be present in `macd`; `mackube` does not pull it. The
adapter supports up to four Pods with one container each. The example uses a
single replica, `Recreate`, a node selector, and a matching toleration to place
the Pod on the Mac node. The Pod uses host networking, so it has no separate
Pod IP and a selector-based Kubernetes Service cannot route to it.

The Pod has the image's default entrypoint, no environment overrides, and
one read-only `hostPath` directory mounted at an image path that does not
already exist. The directory is read from the Mac running `macd`. Image pull
secrets, probes, exec, port forwarding, and metrics are not supported. Native
Pod logs support `kubectl logs`, `--tail`, `-f`, `--since`, and `--timestamps`,
including their combination used by Stern. The `--previous` and `--limit-bytes`
options are not yet supported. Log history is held in memory, up to 1 MiB per
container; tail and since selection use that retained history.

Kubernetes `Ready` currently means the process is alive; the model can still
be loading. Check `/health` before sending inference requests. A deleted
native Pod can stay listed with status `Deleted` after its process has
stopped. Both `macd` and `mackube` keep state in memory.

For the larger LiveKit setup, see the [voice cluster guide](VOICE_CLUSTER.md).
