FROM scratch AS build
COPY --from=node-toolchain / /node/
COPY --from=agent-source /agent/ /app/agent/
COPY --from=agent-source /speech.env /app/speech.env
WORKDIR /app
RUN mkdir -p home tmp \
    && app_dir="$(pwd -P)" \
    && node="$(dirname "$app_dir")/node/bin/node" \
    && HOME="$app_dir/home" TMPDIR="$app_dir/tmp" \
       PATH="$(dirname "$node"):/usr/bin:/bin" \
       "$node" "$(dirname "$app_dir")/node/lib/node_modules/npm/bin/npm-cli.js" \
         ci --prefix "$app_dir/agent"
RUN app_dir="$(pwd -P)" \
    && node="$(dirname "$app_dir")/node/bin/node" \
    && "$node" agent/node_modules/typescript/bin/tsc -p agent/tsconfig.json
RUN app_dir="$(pwd -P)" \
    && node="$(dirname "$app_dir")/node/bin/node" \
    && cd agent \
    && HOME="$app_dir/home" TMPDIR="$app_dir/tmp" \
       "$node" node_modules/@livekit/agents/dist/bin/livekit-agents.js download-files
RUN cp -RL agent/node_modules agent/node_modules.flat \
    && rm -rf agent/node_modules \
    && mv agent/node_modules.flat agent/node_modules \
    && mkdir -p home.flat/.cache \
    && cp -RL home/.cache/huggingface home.flat/.cache/huggingface \
    && rm -rf home \
    && mv home.flat home \
    && rm -rf tmp

FROM scratch
WORKDIR /app
COPY --from=voice-launcher /launch ./bin/launch
COPY --from=node-toolchain /bin/node ./bin/node
COPY --from=build /app/agent/ ./agent/
COPY --from=build /app/speech.env ./speech.env
COPY --from=build /app/home/ ./home/
COPY launch.json ./launch.json
EXPOSE 8083
ENTRYPOINT ["./bin/launch"]
