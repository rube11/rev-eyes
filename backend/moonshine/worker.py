"""Go-owned Moonshine process. Input: uint32 LE length + 16kHz mono PCM16.

Output: JSON lines. Audio remains in a bounded RAM ring and is never saved.
"""
import array
import base64
import json
import os
import struct
import sys

from moonshine_voice import Transcriber
from moonshine_voice.download import get_model_for_language
from moonshine_voice.moonshine_api import ModelArch
from moonshine_voice.transcriber import LineTextChanged, LineCompleted

RATE_BYTES = 32000
RING_BYTES = 32 * RATE_BYTES


def emit(value):
    print(json.dumps(value), flush=True)


def main():
    model_path = os.environ.get("MOONSHINE_MODEL_PATH")
    if not model_path:
        model_path, _ = get_model_for_language("en", ModelArch.TINY_STREAMING)
    transcriber = Transcriber(model_path, ModelArch.TINY_STREAMING, update_interval=0.5)
    stream = transcriber.create_stream()
    ring = bytearray()
    total = 0

    def event_received(event):
        if not isinstance(event, (LineTextChanged, LineCompleted)):
            return
        line = event.line
        final = isinstance(event, LineCompleted)
        message = {"type": "moonshine_transcript", "id": str(line.line_id),
                   "text": line.text, "final": final}
        if final:
            # Include 250ms of context on both sides, within the retained ring.
            start = max(0, int((line.start_time - 0.25) * 16000)) * 2
            end = min(total, int((line.start_time + line.duration + 0.25) * 16000) * 2)
            retained_start = total - len(ring)
            if start < retained_start or end <= start or end - start > 30 * RATE_BYTES:
                message["error"] = "Speech exceeded the 30-second clip limit; pause between phrases"
            else:
                message["pcm"] = base64.b64encode(ring[start-retained_start:end-retained_start]).decode("ascii")
        emit(message)

    stream.add_listener(event_received)
    stream.start()
    emit({"type": "ready", "engine": "moonshine", "sample_rate": 16000})
    while True:
        header = sys.stdin.buffer.read(4)
        if not header:
            break
        if len(header) != 4:
            raise ValueError("Incomplete PCM header")
        size, = struct.unpack("<I", header)
        if size == 0:
            break
        if size > RATE_BYTES or size % 2:
            raise ValueError("Invalid PCM frame size")
        pcm = sys.stdin.buffer.read(size)
        if len(pcm) != size:
            raise ValueError("Incomplete PCM frame")
        ring.extend(pcm)
        total += size
        if len(ring) > RING_BYTES:
            del ring[:len(ring)-RING_BYTES]
        samples = array.array("h", pcm)
        if sys.byteorder != "little":
            samples.byteswap()
        stream.add_audio([sample / 32768.0 for sample in samples], 16000)
    stream.stop()
    emit({"type": "stopped", "received_bytes": total})


if __name__ == "__main__":
    main()
