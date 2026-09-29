FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS assets
WORKDIR /src/cmd/web
COPY cmd/web/package.json cmd/web/package-lock.json ./
RUN npm ci
COPY cmd/web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS compile
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/web/ ./cmd/web/
COPY --from=assets /src/cmd/web/dist/ ./cmd/web/dist/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o /out/web ./cmd/web

FROM scratch
COPY --from=compile /out/web /web
EXPOSE 8090
ENTRYPOINT ["/web", "-addr", "0.0.0.0:8090", "-livekit-url", "ws://localhost:7880"]
