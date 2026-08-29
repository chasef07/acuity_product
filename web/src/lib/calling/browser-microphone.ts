import type { SoftphoneMicrophone } from "./softphone-runtime"

export function createBrowserMicrophone(): SoftphoneMicrophone {
  return {
    async start(onUnavailable, signal) {
      const mediaDevices = navigator.mediaDevices
      if (!mediaDevices?.getUserMedia) {
        throw new Error("browser microphone is unavailable")
      }

      let stream: MediaStream | undefined
      let audioContext: AudioContext | undefined
      let verifyDevices: (() => Promise<void>) | undefined
      let stopped = false
      const stop = () => {
        if (stopped) return
        stopped = true
        if (verifyDevices) {
          mediaDevices.removeEventListener?.("devicechange", verifyDevices)
        }
        stream?.getTracks().forEach((track) => track.stop())
        if (audioContext && audioContext.state !== "closed") {
          void audioContext.close().catch(() => undefined)
        }
      }
      let rejectAbort: ((error: DOMException) => void) | undefined
      const aborted = new Promise<never>((_resolve, reject) => {
        rejectAbort = reject
      })
      const onAbort = () => {
        stop()
        rejectAbort?.(new DOMException("microphone startup aborted", "AbortError"))
      }
      signal?.addEventListener("abort", onAbort, { once: true })

      try {
        if (signal?.aborted) {
          throw new DOMException("microphone startup aborted", "AbortError")
        }
        const streamRequest = mediaDevices.getUserMedia({ audio: true })
        void streamRequest.then((lateStream) => {
          if (signal?.aborted) {
            lateStream.getTracks().forEach((track) => track.stop())
          }
        }, () => undefined)
        stream = await Promise.race([streamRequest, aborted])
        const microphone = stream.getAudioTracks()[0]
        if (!microphone || microphone.readyState !== "live") {
          throw new Error("browser microphone is unavailable")
        }

        let unavailable = false
        const reportUnavailable = () => {
          if (unavailable) return
          unavailable = true
          onUnavailable()
        }
        microphone.addEventListener("ended", reportUnavailable, { once: true })
        verifyDevices = async () => {
          const devices = await mediaDevices.enumerateDevices().catch(() => [])
          if (!devices.some((device) => device.kind === "audioinput")) {
            reportUnavailable()
          }
        }
        mediaDevices.addEventListener?.("devicechange", verifyDevices)

        audioContext = new AudioContext()
        await Promise.race([
          audioContext.resume().then(() => audioContext?.close()),
          aborted,
        ])
        audioContext = undefined
        signal?.removeEventListener("abort", onAbort)
        return { stop }
      } catch (error) {
        signal?.removeEventListener("abort", onAbort)
        stop()
        throw error
      }
    },
  }
}
