FROM scratch AS build
COPY --from=uv-toolchain /uv /tools/uv
COPY --from=kokoro-source / /app/kokoro/
WORKDIR /app
RUN mkdir -p home tmp cache python \
    && app_dir="$(pwd -P)" \
    && HOME="$app_dir/home" TMPDIR="$app_dir/tmp" \
       UV_CACHE_DIR="$app_dir/cache" \
       UV_PYTHON_INSTALL_DIR="$app_dir/python" \
       UV_NO_PROGRESS=1 \
       "$(dirname "$app_dir")/tools/uv" python install 3.12 --no-bin \
    && rm "$app_dir/python/cpython-3.12-macos-aarch64-none" \
    && ln -s cpython-3.12.13-macos-aarch64-none \
         "$app_dir/python/cpython-3.12-macos-aarch64-none"
RUN app_dir="$(pwd -P)" \
    && HOME="$app_dir/home" TMPDIR="$app_dir/tmp" \
       UV_CACHE_DIR="$app_dir/cache" \
       UV_PYTHON_INSTALL_DIR="$app_dir/python" \
       "$(dirname "$app_dir")/tools/uv" venv \
         --python "$app_dir/python/cpython-3.12.13-macos-aarch64-none/bin/python3.12" \
         --relocatable --no-python-downloads "$app_dir/venv"
RUN app_dir="$(pwd -P)" \
    && HOME="$app_dir/home" TMPDIR="$app_dir/tmp" \
       UV_CACHE_DIR="$app_dir/cache" UV_LINK_MODE=copy UV_NO_PROGRESS=1 \
       "$(dirname "$app_dir")/tools/uv" pip install \
         --python "$app_dir/venv/bin/python" "$app_dir/kokoro"
RUN cp -RL python python.flat \
    && rm -rf python \
    && mv python.flat python \
    && cp -RL venv/lib/python3.12/site-packages site-packages \
    && rm -rf cache tmp

FROM scratch
WORKDIR /app
COPY --from=voice-launcher /launch ./bin/launch
COPY --from=build /app/python/ ./python/
COPY --from=build /app/site-packages/ ./site-packages/
COPY --from=build /app/kokoro/ ./kokoro/
COPY --from=kokoro-weights /kokoro-v1_0.pth ./kokoro/api/src/models/v1_0/kokoro-v1_0.pth
COPY launch.json ./launch.json
EXPOSE 8880
ENTRYPOINT ["./bin/launch"]
