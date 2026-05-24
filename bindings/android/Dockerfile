# bindings/android/Dockerfile
#
# Builds waengine.aar for Android.
# Go 1.25 is required because gomobile@latest now requires >= 1.25.
#
# Build:
#   docker build -f bindings/android/Dockerfile -t wa-engine-builder .
#   docker run --name temp-builder wa-engine-builder
#   docker cp temp-builder:/workspace/bindings/android/build/waengine.aar ./bindings/android/build/waengine.aar
#   docker rm temp-builder

FROM --platform=linux/amd64 golang:1.25-bookworm

# ── System deps ───────────────────────────────────────────────────────────────
RUN apt-get update && apt-get install -y --no-install-recommends \
        openjdk-17-jdk-headless \
        unzip \
        curl \
        gcc \
        git \
    && rm -rf /var/lib/apt/lists/*

# ── Android SDK + NDK ─────────────────────────────────────────────────────────
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

# ── gomobile ──────────────────────────────────────────────────────────────────
# @latest works now because we are on Go 1.25
ENV GOPATH=/go
ENV PATH="${GOPATH}/bin:${PATH}"

RUN go install golang.org/x/mobile/cmd/gomobile@latest && \
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
