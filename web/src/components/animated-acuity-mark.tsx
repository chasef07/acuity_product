"use client"

import { useEffect, useRef, useState } from "react"

import { acuityMarkDots, dailyAcuityMarkMotion } from "@/lib/acuity-mark-motion"

export function AnimatedAcuityMark({ className }: { className?: string }) {
  const circles = useRef<Array<SVGCircleElement | null>>([])
  const [motion] = useState(() => dailyAcuityMarkMotion(new Date()))

  useEffect(() => {
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return
    let started: number | undefined
    let frame = requestAnimationFrame(function draw(now) {
      started ??= now
      const progress = ((now - started) / 1000 / motion.seconds) % 1
      motion.frame(progress).forEach((dot, index) => {
        const circle = circles.current[index]
        if (!circle) return
        circle.setAttribute("cx", dot.x.toFixed(2))
        circle.setAttribute("cy", dot.y.toFixed(2))
        circle.setAttribute("r", dot.r.toFixed(2))
        circle.setAttribute("opacity", dot.opacity.toFixed(2))
      })
      frame = requestAnimationFrame(draw)
    })
    return () => cancelAnimationFrame(frame)
  }, [motion])

  return (
    <svg
      viewBox="0 0 32 32"
      fill="currentColor"
      overflow="visible"
      role="img"
      aria-label="Acuity"
      data-motion={motion.name}
      className={className}
    >
      {acuityMarkDots.map(([x, y], index) => (
        <circle
          key={index}
          ref={(circle) => {
            circles.current[index] = circle
          }}
          cx={x}
          cy={y}
          r="2.75"
        />
      ))}
    </svg>
  )
}
