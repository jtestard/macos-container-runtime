FROM scratch AS build

# Both contexts are local directories supplied by the Buildx command.
# The Go distribution is present in the build stage, not borrowed from the host.
COPY --from=go-toolchain / /toolchain/

WORKDIR /app
COPY --from=app /go.mod /go.sum ./
COPY --from=app /cmd/server ./cmd/server
COPY --from=app /src ./src
COPY --from=app /include ./include
COPY --from=app /lib ./lib

# The prototype executes the host shell in a snapshot directory. Derive the
# staged toolchain's physical path until the worker has image-root semantics.
RUN mkdir -p .go-cache .go-modcache .gopath .tmp .home bin \
    && build_dir="$(pwd -P)" \
    && image_root="$(dirname "$build_dir")" \
    && GOROOT="$image_root/toolchain" GOCACHE="$build_dir/.go-cache" \
       GOMODCACHE="$build_dir/.go-modcache" GOPATH="$build_dir/.gopath" \
       TMPDIR="$build_dir/.tmp" HOME="$build_dir/.home" \
       GOTOOLCHAIN=local CGO_ENABLED=1 \
       CC=/Applications/Xcode.app/Contents/Developer/Toolchains/XcodeDefault.xctoolchain/usr/bin/clang \
       PATH=/Applications/Xcode.app/Contents/Developer/Toolchains/XcodeDefault.xctoolchain/usr/bin:/usr/bin:/bin \
       SDKROOT=/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk \
       CGO_LDFLAGS='-Wl,-rpath,@executable_path/../lib' \
       CGO_LDFLAGS_ALLOW='-Wl,-rpath,.*' \
       "$image_root/toolchain/bin/go" build -buildvcs=false -trimpath -ldflags=-w \
         -o bin/server ./cmd/server

FROM scratch
WORKDIR /app
COPY --from=build /app/bin/server ./bin/server
COPY --from=build /app/lib ./lib
COPY --from=app /config.smollm2.toml ./config.smollm2.toml
COPY --from=app /models/smollm2-360m/SmolLM2-360M-Instruct-Q8_0.gguf \
  ./models/smollm2-360m/SmolLM2-360M-Instruct-Q8_0.gguf
EXPOSE 8080
ENTRYPOINT ["./bin/server", "-config", "config.smollm2.toml"]
