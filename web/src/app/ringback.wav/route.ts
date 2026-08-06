const sampleRate = 8_000
const seconds = 20
const toneSeconds = 2
const bytesPerSample = 2
const headerBytes = 44
const audio = ringbackWav()

export function GET() {
  return new Response(audio, {
    headers: {
      "cache-control": "public, max-age=31536000, immutable",
      "content-length": String(audio.byteLength),
      "content-type": "audio/wav",
    },
  })
}

function ringbackWav(): ArrayBuffer {
  const samples = sampleRate * seconds
  const buffer = new ArrayBuffer(headerBytes + samples * bytesPerSample)
  const view = new DataView(buffer)

  writeText(view, 0, "RIFF")
  view.setUint32(4, buffer.byteLength - 8, true)
  writeText(view, 8, "WAVE")
  writeText(view, 12, "fmt ")
  view.setUint32(16, 16, true)
  view.setUint16(20, 1, true)
  view.setUint16(22, 1, true)
  view.setUint32(24, sampleRate, true)
  view.setUint32(28, sampleRate * bytesPerSample, true)
  view.setUint16(32, bytesPerSample, true)
  view.setUint16(34, bytesPerSample * 8, true)
  writeText(view, 36, "data")
  view.setUint32(40, samples * bytesPerSample, true)

  for (let index = 0; index < samples; index += 1) {
    const time = index / sampleRate
    const tone =
      time % 6 < toneSeconds
        ? 0.14 *
          (Math.sin(2 * Math.PI * 440 * time) +
            Math.sin(2 * Math.PI * 480 * time))
        : 0
    view.setInt16(headerBytes + index * bytesPerSample, tone * 32_767, true)
  }
  return buffer
}

function writeText(view: DataView, offset: number, value: string) {
  for (let index = 0; index < value.length; index += 1) {
    view.setUint8(offset + index, value.charCodeAt(index))
  }
}
