import type { ReactNode } from "react"

import { StoryArt } from "./story-art"
import type { StoryArt as StoryArtKind } from "./stories"
import styles from "./platform.module.css"

export type AtmosphereTone = "mist" | "sand" | "stone" | "dusk" | "ember"
export type AtmosphereShape = "a" | "b" | "c"

export function Atmosphere({
  tone,
  shape = "a",
  art,
  className,
  children,
}: {
  tone: AtmosphereTone
  shape?: AtmosphereShape
  art?: StoryArtKind
  className?: string
  children?: ReactNode
}) {
  return (
    <div className={`${styles.atmosphere} ${className ?? ""}`} data-tone={tone} data-shape={shape}>
      {art ? (
        <span className={styles.atmosphereArt}>
          <StoryArt kind={art} bare />
        </span>
      ) : null}
      {children}
    </div>
  )
}
