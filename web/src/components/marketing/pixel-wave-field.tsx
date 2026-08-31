"use client"

import { useEffect, useRef } from "react"

import styles from "./pixel-wave-field.module.css"

export function PixelWaveField() {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    const context = canvas.getContext("2d", { alpha: true })
    if (!context) return

    let width = 0
    let height = 0
    let frame = 0
    let start = performance.now()
    let lastFrame = start
    let pointerX = 0.58
    let pointerY = 0.46
    let pointerTargetX = pointerX
    let pointerTargetY = pointerY
    let pointerVelocityX = 0
    let pointerVelocityY = 0
    let pointerStrength = 0
    let pointerStrengthTarget = 0
    let pointerStrengthVelocity = 0
    const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches

    const resize = () => {
      const rect = canvas.getBoundingClientRect()
      const ratio = Math.min(window.devicePixelRatio || 1, 1.5)
      width = Math.max(1, rect.width)
      height = Math.max(1, rect.height)
      canvas.width = Math.round(width * ratio)
      canvas.height = Math.round(height * ratio)
      context.setTransform(ratio, 0, 0, ratio, 0, 0)
      start = performance.now()
      lastFrame = start
    }

    const onPointerMove = (event: PointerEvent) => {
      const rect = canvas.getBoundingClientRect()
      pointerTargetX = (event.clientX - rect.left) / rect.width
      pointerTargetY = (event.clientY - rect.top) / rect.height
      pointerStrengthTarget = 1
    }

    const onPointerLeave = () => {
      pointerStrengthTarget = 0
    }

    const draw = (now: number) => {
      const delta = Math.min(1 / 30, Math.max(1 / 240, (now - lastFrame) / 1000))
      lastFrame = now

      if (!reducedMotion) {
        const positionStiffness = 86
        const positionDamping = 14
        pointerVelocityX +=
          (pointerTargetX - pointerX) * positionStiffness * delta -
          pointerVelocityX * positionDamping * delta
        pointerVelocityY +=
          (pointerTargetY - pointerY) * positionStiffness * delta -
          pointerVelocityY * positionDamping * delta
        pointerX += pointerVelocityX * delta
        pointerY += pointerVelocityY * delta

        const strengthStiffness = 74
        const strengthDamping = 16
        pointerStrengthVelocity +=
          (pointerStrengthTarget - pointerStrength) * strengthStiffness * delta -
          pointerStrengthVelocity * strengthDamping * delta
        pointerStrength += pointerStrengthVelocity * delta
      }

      const elapsed = (now - start) / 1000
      const time = reducedMotion ? 2.4 : elapsed
      const formation = reducedMotion ? 1 : Math.min(1, elapsed / 2.65)
      const columns = Math.max(62, Math.min(92, Math.round(width / 9)))
      const rows = Math.max(38, Math.min(56, Math.round(height / 13)))

      context.clearRect(0, 0, width, height)
      context.fillStyle = "#25334a"

      for (let column = 0; column < columns; column += 1) {
        const u = column / (columns - 1)
        const signedU = u * 2 - 1
        const horizontalEnvelope = Math.pow(Math.sin(u * Math.PI), 0.52)
        const centerX = width * (0.015 + u * 0.97)
        const centerY =
          height *
          (0.455 +
            Math.sin(u * 5.2 + 0.7 + time * 0.14) * 0.075 +
            Math.sin(u * 13.7 - 0.9 - time * 0.08) * 0.028)
        const thickness =
          height *
          (0.028 +
            horizontalEnvelope *
              (0.175 +
                Math.sin(u * 9.6 - 0.4 + time * 0.18) * 0.033 +
                Math.sin(u * 21.2 + 0.8) * 0.014))
        const twist = signedU * 5.4 + Math.sin(u * 8.1) * 0.54 + time * 0.16

        for (let row = 0; row < rows; row += 1) {
          const v = row / (rows - 1)
          const signedV = v * 2 - 1
          const edgeFade = Math.min(1, v * 8, (1 - v) * 8, u * 10, (1 - u) * 10)
          const depth = (Math.sin(twist) * signedV + 1) / 2
          const surfaceRipple =
            Math.sin(signedV * 7.2 + u * 10.8 + time * 0.38) * thickness * 0.075
          const seed = column * 917.31 + row * 471.79
          const scatterX =
            width * (0.02 + ((Math.sin(seed) * 43758.5453) % 1 + 1) % 1 * 0.96)
          const scatterY =
            height * (0.02 + ((Math.sin(seed * 1.731) * 24634.6345) % 1 + 1) % 1 * 0.92)
          const delay = (((Math.sin(seed * 0.371) * 17834.234) % 1 + 1) % 1) * 0.26
          const progress = Math.max(0, Math.min(1, (formation - delay) / (1 - delay)))
          const ease = 1 - Math.pow(1 - progress, 4)
          const baseTargetX =
            centerX +
            signedV * thickness * 0.34 * Math.sin(twist) +
            surfaceRipple * 0.5
          const baseTargetY =
            centerY +
            signedV * thickness * Math.cos(twist) +
            surfaceRipple +
            Math.sin(u * 4.7 + signedV * 2.2 + time * 0.21) * height * 0.009
          const pointerDeltaX = baseTargetX - pointerX * width
          const pointerDeltaY = baseTargetY - pointerY * height
          const pointerDistance = Math.max(0.001, Math.hypot(pointerDeltaX, pointerDeltaY))
          const pointerRadius = Math.max(72, Math.min(width, height) * 0.2)
          const pointerInfluence = Math.exp(
            -(pointerDistance * pointerDistance) /
              (2 * pointerRadius * pointerRadius),
          )
          const pointerPresence = Math.max(0, Math.min(1, pointerStrength))
          const pointerPush = pointerInfluence * pointerPresence * pointerRadius * 0.18
          const targetX =
            baseTargetX + (pointerDeltaX / pointerDistance) * pointerPush
          const targetY =
            baseTargetY + (pointerDeltaY / pointerDistance) * pointerPush
          const x = scatterX + (targetX - scatterX) * ease
          const y = scatterY + (targetY - scatterY) * ease
          const pixel = 1.05 + depth * 0.85
          const settledAlpha = edgeFade * (0.16 + depth * 0.68)
          const alpha = 0.24 + (settledAlpha - 0.24) * ease

          context.globalAlpha = alpha
          context.fillRect(Math.round(x), Math.round(y), pixel, pixel)
        }
      }

      context.globalAlpha = 1
      if (!reducedMotion) frame = requestAnimationFrame(draw)
    }

    const observer = new ResizeObserver(resize)
    observer.observe(canvas)
    canvas.addEventListener("pointermove", onPointerMove)
    canvas.addEventListener("pointerleave", onPointerLeave)
    resize()
    frame = requestAnimationFrame(draw)

    return () => {
      cancelAnimationFrame(frame)
      observer.disconnect()
      canvas.removeEventListener("pointermove", onPointerMove)
      canvas.removeEventListener("pointerleave", onPointerLeave)
    }
  }, [])

  return (
    <div className={styles.field} role="img" aria-label="A pixel wave forming into an abstract voice signal">
      <canvas ref={canvasRef} />
      <p className={styles.caption}>
        <span>AI agents</span>
        <strong>for medical enterprises</strong>
      </p>
    </div>
  )
}
