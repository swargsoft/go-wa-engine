FROM --platform=linux/amd64 golang:1.24-bookworm

# Install system dependencies
RUN apt-get update && apt-get install -y \
    openjdk-17-jdk \
    unzip \
    wget \
    git \
    && rm -rf /var/lib/apt/lists/*

# Android SDK
ENV ANDROID_HOME=/opt/android-sdk
RUN mkdir -p ${ANDROID_HOME}/cmdline-tools && \
    cd ${ANDROID_HOME}/cmdline-tools && \
    wget -q https://dl.google.com/android/repository/commandlinetools-linux-9477386_latest.zip && \
    unzip commandlinetools-linux-9477386_latest.zip && \
    rm commandlinetools-linux-9477386_latest.zip && \
    mv cmdline-tools latest

ENV PATH="${ANDROID_HOME}/cmdline-tools/latest/bin:${PATH}"
RUN yes | sdkmanager --licenses && \
    sdkmanager "ndk;25.1.8937393" "platform-tools" "platforms;android-21"

# gomobile
ENV GOPATH=/go
ENV PATH="${GOPATH}/bin:${PATH}"
RUN go install golang.org/x/mobile/cmd/gomobile@latest && \
    gomobile init -v

WORKDIR /workspace
COPY . /workspace/

# Build the AAR.
# We bind the root package (waengine) which exports:
#   - StartHTTPServer(port int, dataDir string) string
#   - StopHTTPServer()
#   - IsServerRunning() bool
# The PWA communicates with the engine over localhost HTTP —
# no other Go symbols need to be exported to Android.
CMD ["sh", "-c", \
  "go mod download && \
   gomobile bind \
     -v \
     -target=android \
     -androidapi=21 \
     -o bindings/android/build/waengine.aar \
     github.com/mml/wa-engine \
   2>&1"]
