#!/bin/bash
# Manual AAR build without gomobile

set -e

echo "Building wa-engine AAR manually..."

# Create temp directory
TEMP_DIR=$(mktemp -d)
echo "Working in: $TEMP_DIR"

# Generate Java bindings
cd /Users/ravikantpal/CodeProjects/library-workspace/poc/wa-engine
GOPATH=$TEMP_DIR GO111MODULE=on /Users/ravikantpal/go/bin/gobind -lang=java -outdir="$TEMP_DIR/java" github.com/mml/wa-engine

# Generate Go bindings  
GOPATH=$TEMP_DIR GO111MODULE=on /Users/ravikantpal/go/bin/gobind -lang=go -outdir="$TEMP_DIR/go" github.com/mml/wa-engine

echo "Bindings generated in $TEMP_DIR"
ls -la "$TEMP_DIR"
