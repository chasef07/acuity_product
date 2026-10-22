"use client"

import { useEffect, useState } from "react"

const prolongedOutageMilliseconds = 60_000

export function ProlongedOutageNotice() {
  const [visible, setVisible] = useState(false)
  useEffect(() => {
    const timeout = setTimeout(() => setVisible(true), prolongedOutageMilliseconds)
    return () => clearTimeout(timeout)
  }, [])
  if (!visible) return null
  return (
    <p role="status" className="px-1 text-xs text-muted-foreground">
      Live updates paused. Still checking for changes.
    </p>
  )
}
