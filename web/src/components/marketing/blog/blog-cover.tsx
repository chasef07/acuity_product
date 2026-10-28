import type { BlogCoverStyle } from "@/lib/blog"

import { Atmosphere } from "../atmosphere"
import styles from "./blog.module.css"

export function BlogCover({
  cover,
  category,
  className,
}: {
  cover: BlogCoverStyle
  category: string
  className?: string
}) {
  return (
    <Atmosphere
      tone={cover.tone}
      shape={cover.shape}
      art={cover.art}
      className={`${styles.cover} ${className ?? ""}`}
    >
      <span className={styles.coverCategory}>{category}</span>
      <span className={styles.coverLabel}>{cover.label}</span>
    </Atmosphere>
  )
}
