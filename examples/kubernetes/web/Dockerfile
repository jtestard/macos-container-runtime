FROM scratch AS build
COPY --from=go-toolchain / /toolchain/
WORKDIR /app
COPY go.mod main.go ./
RUN mkdir -p .go-cache .gopath .tmp \
    && build_dir="$(pwd -P)" \
    && image_root="$(dirname "$build_dir")" \
    && GOROOT="$image_root/toolchain" GOCACHE="$build_dir/.go-cache" \
       GOPATH="$build_dir/.gopath" TMPDIR="$build_dir/.tmp" \
       GOTOOLCHAIN=local GOPROXY=off CGO_ENABLED=0 \
       "$image_root/toolchain/bin/go" build -buildvcs=false -trimpath -o server . \
    && rm -rf .go-cache .gopath .tmp

FROM scratch
WORKDIR /app
COPY --from=build /app/server ./server
EXPOSE 8081
ENTRYPOINT ["./server"]
