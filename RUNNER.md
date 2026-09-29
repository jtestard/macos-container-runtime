# Local OCI image runner

`cmd/imgrun` launches the tiny web server from a Buildx OCI tarball on this
macOS arm64 host. It does not require Docker Desktop or a Linux VM to run the
binary.

## Run the tiny server

Build the image with the command in
[examples/tiny-web/README.md](examples/tiny-web/README.md). From the repository
root, build and start the runner:

```sh
mkdir -p .build
go build -o .build/imgrun ./cmd/imgrun
.build/imgrun -image .build/tiny-web.tar
```

In another terminal:

```sh
curl http://127.0.0.1:8080/healthz
```

The expected response is `ok`. Press Ctrl-C in the runner terminal to stop
the server. The runner forwards standard input, output, error, SIGINT, and
SIGTERM, returns the process exit status, and removes its temporary image
filesystem after the process exits.

## What it reads and launches

The runner reads an OCI tarball, selects a `darwin/arm64` manifest, verifies
SHA-256 digests and sizes for the manifest, config, and layers, and verifies
the uncompressed layer digests. It applies gzip tar layers in order to a
temporary directory. For this image, it materializes `/app/server`, resolves
the relative entrypoint `./server` from working directory `/app`, checks that
the file is an arm64 Mach-O executable, and starts it under macOS Seatbelt.

Seatbelt restricts ordinary file writes to the temporary run directory.
Network access and host reads remain available. The server binds directly to
host port 8080; `EXPOSE 8080` is metadata and does not publish a port. The
runner has no image-root filesystem mapping, so absolute paths in the program
still refer to the host. Only trusted images should be run with this prototype.

This first runner accepts OCI tarballs with gzip layers containing regular
files and directories. It rejects symlinks, hardlinks, whiteouts, absolute
entrypoints, and non-root image users. It does not fetch from the registry,
provide network or process namespaces, or run the Metal application yet. The
separate [Docker API service](DOCKER_RUNTIME.md) wraps this runner to support
`docker --context macnative run` for the tiny image.
