// Negotiated pcm_speaker_v1: version byte, role byte, then unchanged PCM16LE.
// Frame boundaries and WebSocket ordering keep metadata attached to its samples.
export function encodeAudioFrame(audio: {
  audioPcm: Uint8Array
  speakerRole?: string
  source?: string
}): Uint8Array<ArrayBuffer> {
  const frame = new Uint8Array(audio.audioPcm.length + 2)
  frame[0] = 1
  frame[1] = audio.source === "phone" ? 0 :
    audio.speakerRole === "self" ? 1 : audio.speakerRole === "other" ? 2 : 0
  frame.set(audio.audioPcm, 2)
  return frame
}
