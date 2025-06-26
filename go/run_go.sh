#!/usr/bin/env bash
set -euo pipefail

docker build -t consumer_go .

docker run -it --rm \
  --name consumer_go_webrtc \
  --net host \
  --ipc host \
  --privileged \
  consumer_go "$@"
