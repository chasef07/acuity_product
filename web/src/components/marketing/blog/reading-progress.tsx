"use client"

import { useEffect, useState } from "react"

import styles from "./blog.module.css"

export function ReadingProgress({ targetId }: { targetId: string }) {
  const [progress, setProgress] = useState(0)

  useEffect(() => {
    const update = () => {
      const target = document.getElementById(targetId)
      if (!target) return
      const rect = target.getBoundingClientRect()
      const distance = Math.max(rect.height - window.innerHeight, 1)
      setProgress(Math.min(Math.max(-rect.top / distance, 0), 1))
    }
    update()
    window.addEventListener("scroll", update, { passive: true })
    window.addEventListener("resize", update)
    return () => {
      window.removeEventListener("scroll", update)
      window.removeEventListener("resize", update)
    }
  }, [targetId])

  return (
    <div className={styles.progress} aria-hidden="true">
      <span style={{ transform: `scaleX(${progress})` }} />
    </div>
  )
}
