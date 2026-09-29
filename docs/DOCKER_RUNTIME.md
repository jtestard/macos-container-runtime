# Docker CLI runtime prototype

`cmd/macd` exposes a small Docker Engine API subset over a Unix socket. It
registers one local `darwin/arm64` OCI tarball under a chosen tag and uses
[`imgrun`](RUNNER.md) to execute it. The installed, unmodified Docker CLI can
then issue `docker --context macnative run` on this Mac. Both the tiny web
server and [Metal-backed `go-inf-server`](../examples/go-inf-server/README.md)
have been run through this path.

## Start the service

First build the OCI tarball with the command in
[examples/tiny-web/README.md](../examples/tiny-web/README.md). From the repository
root, build the runner and API service:

```sh
mkdir -p .build
go build -o .build/imgrun ./cmd/imgrun
go build -o .build/macd ./cmd/macd
```

Start the service in its own terminal:

```sh
.build/macd -image .build/tiny-web.tar -tag tiny-web:latest \
  -runner .build/imgrun -socket /private/tmp/macnative-docker.sock
```

The command above registers the tiny web image. The
[`go-inf-server` example](../examples/go-inf-server/README.md) shows the tag and
tarball arguments for the real Metal workload. The service takes a private
copy of the tarball at startup. Restart it after building a new image. It
stores container records in memory for this first version; stopping the
service stops running containers and removes the socket.

Register a Docker context once. This does not change your default context:

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
`stop`, `rm`, `rm -f`, `images`, and `image inspect` worked through this context
with Docker CLI 28.0.4. The foreground CLI attached to native process output and completed after
`docker stop`. The image ID reported by `image inspect` is the OCI config
digest, and its creation time comes from the OCI image config.

## Current boundaries

The service implements only the Engine API requests needed for this workflow.
It accepts one image supplied at startup; there is no `docker load`, image
pull, registry lookup, port mapping, TTY, stdin, entrypoint override, `--rm`,
or daemon persistence yet. It accepts command arguments and read-only `-v`
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
