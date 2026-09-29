# Docker CLI runtime prototype

`cmd/macd` exposes a small Docker Engine API subset over a Unix socket. It
registers one local `darwin/arm64` OCI tarball as `tiny-web:latest` and uses
[`imgrun`](RUNNER.md) to execute it. The installed, unmodified Docker CLI can
then issue `docker --context macnative run` on this Mac.

## Start the service

First build the OCI tarball with the command in
[examples/tiny-web/README.md](examples/tiny-web/README.md). From the repository
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

The service takes a private copy of the tarball at startup. Restart it after
building a new image. It stores container records in memory for this first
version; stopping the service stops running containers and removes the socket.

Register a Docker context once. This does not change your default context:

```sh
docker context create macnative \
  --docker host=unix:///private/tmp/macnative-docker.sock
```

The Buildx **builder** named `macnative` and the Docker **context** named
`macnative` are separate settings: the builder points to the BuildKit socket,
and the context points to this runtime socket.

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

The tested health response is `ok`. `docker run`, `ps`, `logs`, `stop`, `rm`,
and `image inspect` worked through this context with Docker CLI 28.0.4. The
foreground CLI attached to native process output and completed after
`docker stop`. The image ID reported by `image inspect` is the OCI config
digest.

## Current boundaries

The service implements only the Engine API requests needed for this workflow.
It accepts one image supplied at startup; there is no `docker load`, image
pull, registry lookup, volume mounting, port mapping, TTY, stdin, command
override, `--rm`, or daemon persistence yet. Unsupported create options fail
explicitly. The server binds directly to host port 8080, so no `-p` option is
needed or supported.

The existing runner still has no image-root filesystem mapping. Its Seatbelt
profile limits ordinary file writes to the temporary run directory, while
host reads and network access remain available. Docker CLI compatibility here
is an API surface for this trusted-image prototype, not Docker Engine
isolation or full Docker behavior.

## Protocol references

- [Docker Engine API run example](https://docs.docker.com/reference/api/engine/sdk/examples/)
- [Docker contexts](https://docs.docker.com/engine/manage-resources/contexts/)
