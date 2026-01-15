#!/bin/bash
# build-aar.sh - Build Android AAR from wa-engine Go source
#
# This script uses gomobile to generate an Android AAR library
# that can be imported into Android Studio projects.
#
# Prerequisites:
# - Go 1.21+ installed
# - Android SDK installed
# - NDK installed (r21e or later recommended)
# - ANDROID_HOME environment variable set
# - gomobile installed: go install golang.org/x/mobile/cmd/gomobile@latest
# - gomobile initialized: gomobile init
#
# Usage:
#   ./build-aar.sh [output_dir]
#
# Example:
#   ./build-aar.sh ./build
#   # Creates: ./build/waengine.aar

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# Output directory (default: ./build)
OUTPUT_DIR="${1:-${SCRIPT_DIR}/build}"

# AAR filename
AAR_NAME="waengine.aar"

echo -e "${GREEN}=== wa-engine Android AAR Build ===${NC}"
echo ""

# Check prerequisites
echo -e "${YELLOW}Checking prerequisites...${NC}"

# Check Go
if ! command -v go &> /dev/null; then
    echo -e "${RED}Error: Go is not installed${NC}"
    echo "Install Go from https://golang.org/dl/"
    exit 1
fi
GO_VERSION=$(go version | awk '{print $3}')
echo "  ✓ Go: ${GO_VERSION}"

# Check Go version compatibility
GO_MINOR=$(echo "${GO_VERSION}" | sed 's/go1\.\([0-9]*\).*/\1/')
if [ "${GO_MINOR}" -ge 23 ]; then
    echo -e "${YELLOW}  ⚠ Warning: Go 1.23+ has known issues with gomobile${NC}"
    echo -e "${YELLOW}  ⚠ Recommended: Use Go 1.22 or earlier${NC}"
    echo -e "${YELLOW}  ⚠ See bindings/android/KNOWN_ISSUES.md for details${NC}"
    echo ""
fi

# Check ANDROID_HOME
if [ -z "${ANDROID_HOME}" ]; then
    # Try common locations
    if [ -d "$HOME/Library/Android/sdk" ]; then
        export ANDROID_HOME="$HOME/Library/Android/sdk"
    elif [ -d "$HOME/Android/Sdk" ]; then
        export ANDROID_HOME="$HOME/Android/Sdk"
    else
        echo -e "${RED}Error: ANDROID_HOME not set${NC}"
        echo "Set ANDROID_HOME to your Android SDK location"
        exit 1
    fi
fi
echo "  ✓ ANDROID_HOME: ${ANDROID_HOME}"

# Check for NDK
NDK_DIR=""
if [ -d "${ANDROID_HOME}/ndk-bundle" ]; then
    NDK_DIR="${ANDROID_HOME}/ndk-bundle"
elif [ -d "${ANDROID_HOME}/ndk" ]; then
    # Find the latest NDK version
    NDK_DIR=$(ls -d "${ANDROID_HOME}/ndk"/*/ 2>/dev/null | sort -V | tail -1)
fi

if [ -z "${NDK_DIR}" ] || [ ! -d "${NDK_DIR}" ]; then
    echo -e "${RED}Error: Android NDK not found${NC}"
    echo "Install NDK via Android Studio SDK Manager or:"
    echo "  sdkmanager --install 'ndk;25.2.9519653'"
    exit 1
fi
echo "  ✓ NDK: ${NDK_DIR}"

# Check gomobile
if ! command -v gomobile &> /dev/null; then
    echo -e "${YELLOW}Installing gomobile...${NC}"
    go install golang.org/x/mobile/cmd/gomobile@latest
fi
echo "  ✓ gomobile: $(which gomobile)"

# Initialize gomobile if needed
echo ""
echo -e "${YELLOW}Initializing gomobile...${NC}"
gomobile init

# Create output directory
mkdir -p "${OUTPUT_DIR}"

# Change to project root
cd "${PROJECT_ROOT}"

# Download dependencies
echo ""
echo -e "${YELLOW}Downloading dependencies...${NC}"
go mod download
go mod tidy

# Build AAR
echo ""
echo -e "${YELLOW}Building AAR...${NC}"
echo "  Target: android"
echo "  Output: ${OUTPUT_DIR}/${AAR_NAME}"
echo ""

# Build with gomobile
# -target=android: Build for Android
# -androidapi=21: Minimum API level (Android 5.0)
# -o: Output file path
GO111MODULE=on gomobile bind \
    -target=android \
    -androidapi=21 \
    -o "${OUTPUT_DIR}/${AAR_NAME}" \
    -v \
    github.com/mml/wa-engine

# Check if build succeeded
if [ -f "${OUTPUT_DIR}/${AAR_NAME}" ]; then
    echo ""
    echo -e "${GREEN}=== Build Successful ===${NC}"
    echo ""
    echo "AAR file: ${OUTPUT_DIR}/${AAR_NAME}"
    echo "Size: $(du -h "${OUTPUT_DIR}/${AAR_NAME}" | cut -f1)"
    echo ""
    echo "To use in Android Studio:"
    echo "  1. Copy ${AAR_NAME} to your app's libs/ directory"
    echo "  2. Add to build.gradle:"
    echo "     implementation files('libs/${AAR_NAME}')"
    echo "  3. Sync project with Gradle"
    echo ""
    echo "Example usage in Kotlin:"
    echo "  import waengine.Waengine"
    echo ""
    echo "  // Initialize"
    echo "  Waengine.newEngine(context.filesDir.absolutePath + \"/whatsapp\")"
    echo ""
    echo "  // Start connection"
    echo "  Waengine.start()"
    echo ""
    echo "  // Poll for events"
    echo "  val event = Waengine.pollEvent()"
    echo ""
else
    echo -e "${RED}Build failed: AAR not created${NC}"
    exit 1
fi
