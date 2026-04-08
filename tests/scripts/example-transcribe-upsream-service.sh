#!/bin/bash
PORT=8007  # our development server
INFERENCE_PATH=v1/audio/transcriptions

case ${container:-} in
    docker|podman)
        HOST=host.docker.internal
        ;;
    *)
        HOST=localhost
        ;;
esac
      
if [ "$#" = 0 ]; then
    WAV_FILE="$(dirname $0)/../test-data/LibriSpeech/2961-960-0000.wav"
else
    WAV_FILE="$1"
fi


curl \
    -4 $HOST:$PORT/$INFERENCE_PATH \
    -H "Content-Type: multipart/form-data" \
    -F file="@$WAV_FILE" \
    -F model="whisper-1" \
    "${@:2}"

printf '\n'  # for prettier terminal, server doesn not add newline to json response...
