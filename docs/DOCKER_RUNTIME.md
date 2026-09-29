# Docker CLI runtime prototype

`cmd/macd` exposes a small Docker Engine API subset over a Unix socket. It
pulls `darwin/arm64` images from a registry into a persistent store and uses
[`imgrun`](RUNNER.md) to execute it. The installed, unmodified Docker CLI can
then issue `docker --context macnative run` on this Mac. Both the tiny web
server and [Metal-backed `go-inf-server`](../examples/go-inf-server/README.md)
have been run through this path.

## Start the service

From the repository root, install the runner and API service:

```sh
./install.sh
./scripts/enable-macd.sh
```

`install.sh` builds both binaries under `~/Library/Application Support/macnative/bin`,
prepares `~/Library/Application Support/macnative/images`, and creates the Docker
context. `scripts/enable-macd.sh` installs a user LaunchAgent with `RunAtLoad`
and `KeepAlive`. It starts at login, independently of Docker Desktop. Use
`./scripts/disable-macd.sh` to stop and remove the agent. Logs are in the
same Application Support directory under `logs/`.

For a manual service in its own terminal, run:

```sh
"$HOME/Library/Application Support/macnative/bin/macd" \
  -runner "$HOME/Library/Application Support/macnative/bin/imgrun" \
  -store "$HOME/Library/Application Support/macnative/images" \
  -socket /private/tmp/macnative-docker.sock
```

Stop the LaunchAgent before starting the manual service on the same socket.
The older `-image local.tar -tag name:tag` flags remain available to register
a local OCI tarball at startup. The service takes a private copy of that
tarball. Pulled images persist across restarts; container records remain in
memory, and stopping the service stops its running containers.

The installer registers a Docker context once. This does not change your
default context; to create it manually:

```sh
docker context create macnative \
  --docker host=unix:///private/tmp/macnative-docker.sock
```

The Buildx **builder** named `macnative` and the Docker **context** named
`macnative` are separate settings: the builder points to the BuildKit socket,
and the context points to this runtime socket.

Docker commands query one context at a time. If your default context is
`desktop-linux`, plain `docker ps` shows Docker Desktop containers, not native
containers. Use `docker --context macnative ps -a` to see the native containers.
This also explains why `docker --context macnative run --name tiny-web` reports
that the name exists after a detached run, even when plain `docker ps` does not
show it.

To make plain `docker ps`, `docker logs`, and other commands use the native
runtime in the current shell session, set:

```sh
export DOCKER_CONTEXT=macnative
docker ps
```

Run `unset DOCKER_CONTEXT` to return to your default context. Alternatively,
keep using `docker --context macnative ...` on each command. Neither approach
combines the two daemons' container lists.

## Run the image

Pull a trusted Darwin image before running it:

```sh
docker --context macnative pull jtstormz/tiny-web:llama-server-001
docker --context macnative images
docker --context macnative image inspect jtstormz/tiny-web:llama-server-001
```

The registry selection is fixed to `darwin/arm64`. Docker CLI credentials
from `docker login` and its credential helper are used for authenticated
registries. Linux-only images fail with a platform error. Pulled images are
stored in the user's macnative image store. `docker pull --platform darwin/arm64`
is also accepted.

The following commands use the locally built `tiny-web:latest` example. Start
`macd` manually with `-image .build/tiny-web.tar -tag tiny-web:latest`, or use
the pulled image name above instead.

For a foreground process:

```sh
docker --context macnative run --name tiny-web tiny-web:latest
```

From another terminal, check it and stop it:

```sh
curl http://127.0.0.1:8080/healthz
docker --context macnative stop tiny-web
docker --context macnative rm tiny-web
```

For a detached process:

```sh
docker --context macnative run -d --name tiny-web tiny-web:latest
docker --context macnative ps
docker --context macnative logs tiny-web
docker --context macnative stop tiny-web
docker --context macnative rm tiny-web
```

The tiny server logs every HTTP request. To follow new requests live in a
second terminal:

```sh
docker --context macnative logs -f tiny-web
```

For a running container, `docker --context macnative rm -f tiny-web` kills the
server, waits for runner cleanup, and removes the container record in one
command. It also closes an active `logs -f` stream.

The tested health response is `ok`. `docker run`, `ps`, `logs`, `logs -f`,
`stop`, `rm`, `rm -f`, `pull`, `images`, and `image inspect` worked through this context
with Docker CLI 28.0.4. The foreground CLI attached to native process output and completed after
`docker stop`. The image ID reported by `image inspect` is the OCI config
digest, and its creation time comes from the OCI image config.

## Current boundaries

The service implements only the Engine API requests needed for this workflow.
It accepts multiple pulled images and one optional image supplied at startup.
There is no `docker load`, port mapping, TTY, stdin, entrypoint override,
`--rm`, or container persistence yet. It accepts command arguments and read-only `-v`
binds whose host source already exists. Bind targets must be absent in the
image. Unsupported create options fail explicitly. The tiny web example binds
directly to host port 8080, so no `-p` option is needed or supported.

The existing runner still has no image-root filesystem mapping. Its Seatbelt
profile limits ordinary file writes to the temporary run directory, while
host reads and network access remain available. A mounted directory is
addressed through a relative path from the image working directory because
the runner does not remap absolute image paths. Runtime processes use the
Mac's host network; the service does not provide a bridge or network namespace.
Docker CLI compatibility here
is an API surface for this trusted-image prototype, not Docker Engine
isolation or full Docker behavior.

## Protocol references

- [Docker Engine API run example](https://docs.docker.com/reference/api/engine/sdk/examples/)
- [Docker contexts](https://docs.docker.com/engine/manage-resources/contexts/)
