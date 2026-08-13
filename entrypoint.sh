#!/bin/sh
set -e

# Render injects PORT (default 10000 for web services); the app reads
# HTTP_ADDR. Map PORT onto HTTP_ADDR so the container listens on the port
# Render expects, while keeping the local default (:8080) intact.
export HTTP_ADDR=":${PORT:-8080}"

exec /usr/local/bin/agro-iam