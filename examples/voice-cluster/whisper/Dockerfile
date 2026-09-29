FROM scratch AS build
COPY --from=whisper-source / /src/
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
         -DWHISPER_BUILD_TESTS=OFF \
    && HOME="$build_dir/.home" TMPDIR="$build_dir/.tmp" \
       "$image_root/cmake/bin/cmake" --build "$build_dir/output" --target whisper-server -j 4

FROM scratch
WORKDIR /app
COPY --from=build /work/output/bin/whisper-server ./bin/whisper-server
COPY --from=whisper-model /ggml-base.en.bin ./models/ggml-base.en.bin
EXPOSE 8000
ENTRYPOINT ["./bin/whisper-server", "-m", "models/ggml-base.en.bin", "--host", "127.0.0.1", "--port", "8000", "--inference-path", "/v1/audio/transcriptions", "-l", "en", "-nt", "-t", "4"]
