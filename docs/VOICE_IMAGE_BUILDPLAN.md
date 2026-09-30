# Voice stack image build plan

Goal: build and run the complete local voice stack from images. LiveKit and
the web UI run as Linux Pods in Kind. The LLM, Whisper, Kokoro, and the voice
agent run as `darwin/arm64` Pods on the Mac so their native dependencies and
Metal access remain available.

## Progress

- [x] Inventory current binaries, models, module trees, and runtime limits.
- [x] Build a Linux/arm64 web UI image and run it in Kind; health and config responded through a temporary port forward.
- [x] Recreate the dedicated Kind cluster with the new 8090/TCP mapping and move the browser to the Pod.
- [x] Build a Darwin/arm64 Whisper image from source with Metal and the model embedded; run it as a virtual Pod and pass the speech round trip.
- [x] Build a Darwin/arm64 voice-agent image with Node and local models embedded.
- [x] Build a Darwin/arm64 Kokoro image with Python, packages, weights, and voices embedded.
- [x] Run four one-image `macd` instances and four one-Pod virtual nodes with distinct workload labels.
- [x] Replace the four native host processes with Pods; verify room dispatch, greeting synthesis, the STT/TTS round trip, and an automated spoken turn through STT, LLM, and TTS.
- [ ] Verify a spoken browser turn with a microphone.

## Current constraints

The native BuildKit worker executes host shell commands in a writable
snapshot. It does not remap `/` into an image root. Dockerfiles must locate
their staged toolchains through the snapshot path and keep build writes
inside that snapshot. The OCI runner extracts layers into a temporary root
but accepts only regular files and directories, requires a relative Mach-O
entrypoint, and does not translate absolute image paths in arguments or
environment variables. Python virtual environments and many Node packages
contain symlinks and absolute paths. Packaging them requires validating those
behaviors before scheduling a Pod.

The initial voice stack used one `macd` socket and virtual node per image
because `mackube` allowed one native Pod. The current setup registers all
four images in one `macd` and schedules their Pods on one node. The services
listen on distinct host ports so their processes can run concurrently.
Kubernetes Service routing to a native Pod is still absent. The agent can use
the current localhost endpoints while all native Pods share the Mac network.
