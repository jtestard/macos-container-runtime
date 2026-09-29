# Runtime install and Docker pull build plan

Goal: install the native runtime once, start `macd` automatically for the
current macOS user, and run `docker --context macnative pull` followed by
`docker --context macnative run` for a trusted `darwin/arm64` image.

## Progress

- [x] Keep multiple images in a persistent local store and pin each container
  to the image selected at creation.
- [x] Implement Docker Engine `POST /images/create` for `darwin/arm64` registry
  pulls, including Docker credential support and CLI progress/error responses.
- [x] Verify pull, image listing, inspect, run, and restart persistence through
  a separate test socket without changing the running voice cluster.
- [x] Add `install.sh` to build and install `macd` and `imgrun`, create the
  Docker context, and prepare the image store.
- [x] Add a separate script that installs a user LaunchAgent to start `macd`
  at login and keep it running. Document how to load and stop it.
- [x] Update the README and runtime guide with the new command sequence and
  remaining limits.

Verified with `jtstormz/tiny-web:llama-server-001`: Docker CLI pull, images,
inspect, run with a read-only model bind, HTTP 200 from `/health`, and image
inspect after daemon restart. The installer was tested with an isolated prefix;
the startup script generated a plist that passed `plutil -lint`. The
LaunchAgent was not enabled for the main user by the build. This adds registry pulls, not a Linux container runtime
or general Docker image compatibility. The runner still requires a
`darwin/arm64` image with supported layer contents.
