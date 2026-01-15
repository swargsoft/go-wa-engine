FROM --platform=linux/amd64 golang:1.24-bookworm

# Install dependencies
RUN apt-get update && apt-get install -y \
    openjdk-17-jdk \
    unzip \
    wget \
    git \
    && rm -rf /var/lib/apt/lists/*

# Install Android SDK command-line tools
ENV ANDROID_HOME=/opt/android-sdk
RUN mkdir -p ${ANDROID_HOME}/cmdline-tools && \
    cd ${ANDROID_HOME}/cmdline-tools && \
    wget -q https://dl.google.com/android/repository/commandlinetools-linux-9477386_latest.zip && \
    unzip commandlinetools-linux-9477386_latest.zip && \
    rm commandlinetools-linux-9477386_latest.zip && \
    mv cmdline-tools latest

# Install Android NDK
ENV PATH="${ANDROID_HOME}/cmdline-tools/latest/bin:${PATH}"
RUN yes | sdkmanager --licenses && \
    sdkmanager "ndk;25.1.8937393" "platform-tools" "platforms;android-21"

# Install gomobile (latest version works with Go 1.24+)
ENV GOPATH=/go
ENV PATH="${GOPATH}/bin:${PATH}"
RUN go install golang.org/x/mobile/cmd/gomobile@latest && \
    gomobile init -v

WORKDIR /workspace

# Copy the source
COPY . /workspace/

# Command to build AAR at runtime
CMD ["sh", "-c", "go mod download && go get golang.org/x/mobile/bind && gomobile bind -v -target=android -androidapi=21 -o bindings/android/build/waengine.aar . 2>&1"]
