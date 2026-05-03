#!/usr/bin/env bash
# download-models.sh
# Downloads the minimum required models for c2 voice mode.
#
# VAD:  Silero VAD (~2MB)
# STT:  Whisper tiny.en (offline, ~70MB, int8 quantized)

set -euo pipefail

MODELS_DIR="${HOME}/.c2/models"
STT_DIR="${MODELS_DIR}/stt"
VAD_VERSION="asr-models"
STT_VERSION="asr-models"
STT_MODEL="sherpa-onnx-whisper-tiny.en"

mkdir -p "${MODELS_DIR}" "${STT_DIR}"

# --- VAD: Silero ---
VAD_DEST="${MODELS_DIR}/silero_vad.onnx"
if [ -f "${VAD_DEST}" ]; then
    echo "==> VAD model already present, skipping."
else
    echo "==> Downloading Silero VAD..."
    curl -fL --progress-bar \
        -o "${VAD_DEST}" \
        "https://github.com/k2-fsa/sherpa-onnx/releases/download/${VAD_VERSION}/silero_vad.onnx"
    echo "    saved to ${VAD_DEST}"
fi

# --- STT: Whisper tiny.en (int8 quantized) ---
if [ -f "${STT_DIR}/tiny.en-encoder.int8.onnx" ]; then
    echo "==> STT model already present, skipping."
else
    echo "==> Downloading Whisper tiny.en STT model (~70MB)..."
    ARCHIVE="${STT_MODEL}.tar.bz2"
    TMPDIR=$(mktemp -d)
    trap 'rm -rf "${TMPDIR}"' EXIT

    curl -fL --progress-bar \
        -o "${TMPDIR}/${ARCHIVE}" \
        "https://github.com/k2-fsa/sherpa-onnx/releases/download/${STT_VERSION}/${ARCHIVE}"

    echo "==> Extracting..."
    tar -xjf "${TMPDIR}/${ARCHIVE}" -C "${STT_DIR}" --strip-components=1

    echo "    saved to ${STT_DIR}/"
fi

echo ""
echo "==> Models ready. Add the following to the \"c2\" section of ~/.c2/config.json:"
echo ""
cat <<EOF
  "c2": {
    "vad_model":    "~/.c2/models/silero_vad.onnx",
    "stt_encoder":  "~/.c2/models/stt/tiny.en-encoder.int8.onnx",
    "stt_decoder":  "~/.c2/models/stt/tiny.en-decoder.int8.onnx",
    "stt_language": "en"
  }
EOF
