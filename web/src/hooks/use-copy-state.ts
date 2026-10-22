import { useEffect, useState } from "react"

export function useCopyState() {
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle")
  useEffect(() => {
    if (copyState === "idle") return
    const timer = window.setTimeout(() => setCopyState("idle"), 2_000)
    return () => window.clearTimeout(timer)
  }, [copyState])
  return [copyState, setCopyState] as const
}
