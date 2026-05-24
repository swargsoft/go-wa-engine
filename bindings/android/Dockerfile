# bindings/android/Dockerfile
#
# Builds waengine.aar for Android.
# Uses Go 1.25 because golang.org/x/mobile (gomobile) now requires >= 1.25.
# Run via:  docker build --platform=linux/amd64 -f bindings/android/Dockerfile -t wa-engine-builder .
#           docker run --rm -v "$(pwd)/build:/workspace/bindings/android/build" wa-engine-builder

FROM --platform=linux/amd64 golang:1.25-bookworm

# ── System deps ───────────────────────────────────────────────────────────────
RUN apt-get update && apt-get install -y --no-install-recommends \
        openjdk-17-jdk-headless \
        unzip \
        curl \
        gcc \
        git \
    && rm -rf /var/lib/apt/lists/*

# ── Android SDK + NDK ────────────────────────────────────────────────────────
ENV ANDROID_HOME=/opt/android-sdk
ENV ANDROID_NDK_HOME=${ANDROID_HOME}/ndk/25.2.9519653
ENV PATH="${ANDROID_HOME}/cmdline-tools/latest/bin:${ANDROID_HOME}/platform-tools:${PATH}"

RUN mkdir -p ${ANDROID_HOME}/cmdline-tools && \
    curl -fsSL "https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip" \
        -o /tmp/cmdtools.zip && \
    unzip -q /tmp/cmdtools.zip -d ${ANDROID_HOME}/cmdline-tools && \
    mv ${ANDROID_HOME}/cmdline-tools/cmdline-tools ${ANDROID_HOME}/cmdline-tools/latest && \
    rm /tmp/cmdtools.zip

RUN yes | sdkmanager --licenses > /dev/null && \
    sdkmanager "platform-tools" "ndk;25.2.9519653" "platforms;android-34"

# ── Go toolchain (GOTOOLCHAIN=auto so go.mod directives are respected) ────────
ENV GOTOOLCHAIN=auto
ENV GOPATH=/go
ENV PATH="${GOPATH}/bin:${PATH}"

# ── gomobile ─────────────────────────────────────────────────────────────────
# Pin to a specific commit that is known to work with Go 1.25.
# Avoid @latest to prevent future breakage — update intentionally.
RUN go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20250417143005-9fc84da7c9d4 && \
    gomobile init -v

# ── Build ─────────────────────────────────────────────────────────────────────
WORKDIR /workspace
COPY . .

RUN go mod download

RUN mkdir -p bindings/android/build && \
    gomobile bind \
        -target=android \
        -androidapi=21 \
        -o bindings/android/build/waengine.aar \
        -ldflags="-s -w" \
        github.com/mml/wa-engine

# Output is at /workspace/bindings/android/build/waengine.aar
