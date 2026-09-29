FROM scratch AS build
COPY --from=llama-source / /src/
COPY --from=cmake-toolchain / /cmake/
WORKDIR /work
RUN mkdir -p output .home .tmp \
    && build_dir="$(pwd -P)" \
    && image_root="$(dirname "$build_dir")" \
    && HOME="$build_dir/.home" TMPDIR="$build_dir/.tmp" \
       "$image_root/cmake/bin/cmake" -S "$image_root/src" -B "$build_dir/output" \
         -DCMAKE_BUILD_TYPE=Release \
         -DCMAKE_C_COMPILER=/Applications/Xcode.app/Contents/Developer/Toolchains/XcodeDefault.xctoolchain/usr/bin/clang \
         -DCMAKE_CXX_COMPILER=/Applications/Xcode.app/Contents/Developer/Toolchains/XcodeDefault.xctoolchain/usr/bin/clang++ \
         -DCMAKE_MAKE_PROGRAM=/Applications/Xcode.app/Contents/Developer/usr/bin/make \
         -DBUILD_SHARED_LIBS=OFF \
         -DGGML_METAL=ON \
         -DGGML_METAL_EMBED_LIBRARY=ON \
         -DLLAMA_OPENSSL=OFF \
         -DLLAMA_BUILD_UI=OFF \
         -DLLAMA_BUILD_APP=OFF \
         -DLLAMA_BUILD_TOOLS=ON \
         -DLLAMA_BUILD_TESTS=OFF \
         -DLLAMA_BUILD_EXAMPLES=OFF \
         -DLLAMA_BUILD_SERVER=ON \
    && HOME="$build_dir/.home" TMPDIR="$build_dir/.tmp" \
       "$image_root/cmake/bin/cmake" --build "$build_dir/output" --target llama-server -j 4

FROM scratch
WORKDIR /app
COPY --from=build /work/output/bin/llama-server ./bin/llama-server
EXPOSE 8082
ENTRYPOINT ["./bin/llama-server", "--host", "127.0.0.1", "--port", "8082", "--n-gpu-layers", "99"]
