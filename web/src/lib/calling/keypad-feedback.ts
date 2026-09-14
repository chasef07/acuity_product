const toneSources = new Map<string, string>()

// Local confirmation only: never connect this audio to the microphone or RTP.
export function createKeypadFeedback() {
  let active: HTMLAudioElement | undefined

  function stop() {
    const audio = active
    active = undefined
    if (!audio) return
    audio.onended = null
    audio.onerror = null
    audio.pause()
    audio.removeAttribute("src")
    audio.load()
  }

  return {
    stop,
    async play(digit: string, output: HTMLMediaElement) {
      stop()
      const source = toneSource(digit)
      if (!source) return
      const audio = document.createElement("audio")
      active = audio
      audio.src = source
      audio.volume = output.volume
      audio.muted = output.muted
      audio.onended = () => {
        if (active === audio) stop()
      }
      audio.onerror = () => {
        if (active !== audio) return
        stop()
        console.warn("Keypad audio feedback could not play.")
      }
      try {
        // An empty (or unsupported) sinkId means the browser's default output.
        // Route before playing; never fall back to a different speaker on error.
        if (output.sinkId) await audio.setSinkId(output.sinkId)
        if (active !== audio) return
        await audio.play()
      } catch {
        // A newer press or call teardown intentionally aborts pending playback.
        if (active !== audio) return
        stop()
        console.warn("Keypad audio feedback could not play.")
      }
    },
  }
}

function toneSource(digit: string) {
  const keys = ["123A", "456B", "789C", "*0#D"]
  const row = keys.findIndex((keys) => keys.includes(digit))
  if (digit.length !== 1 || row < 0) return
  const cached = toneSources.get(digit)
  if (cached) return cached
  const low = [697, 770, 852, 941][row]
  const high = [1209, 1336, 1477, 1633][keys[row].indexOf(digit)]
  const sampleRate = 8000
  const samples = 960 // 120 ms, with 5 ms fades to avoid clicks.
  const bytes = new Uint8Array(44 + samples * 2)
  const wav = new DataView(bytes.buffer)
  const text = (offset: number, value: string) => {
    for (let i = 0; i < value.length; i++) bytes[offset + i] = value.charCodeAt(i)
  }
  text(0, "RIFF")
  wav.setUint32(4, bytes.length - 8, true)
  text(8, "WAVEfmt ")
  wav.setUint32(16, 16, true)
  wav.setUint16(20, 1, true) // PCM, mono, 16-bit.
  wav.setUint16(22, 1, true)
  wav.setUint32(24, sampleRate, true)
  wav.setUint32(28, sampleRate * 2, true)
  wav.setUint16(32, 2, true)
  wav.setUint16(34, 16, true)
  text(36, "data")
  wav.setUint32(40, samples * 2, true)
  for (let i = 0; i < samples; i++) {
    const fade = Math.min(1, i / 40, (samples - 1 - i) / 40)
    const phase = (2 * Math.PI * i) / sampleRate
    const value = 0.035 * fade * (Math.sin(low * phase) + Math.sin(high * phase))
    wav.setInt16(44 + i * 2, Math.round(value * 32767), true)
  }
  const source = `data:audio/wav;base64,${btoa(String.fromCharCode(...bytes))}`
  toneSources.set(digit, source)
  return source
}
