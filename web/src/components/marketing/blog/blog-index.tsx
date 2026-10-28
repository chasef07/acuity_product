"use client"

import Link from "next/link"
import { useState } from "react"

import type { BlogCategory, BlogCoverStyle } from "@/lib/blog"

import { BlogCover } from "./blog-cover"
import styles from "./blog.module.css"

export type BlogEntry = {
  href: string
  external: boolean
  title: string
  date: string
  dateLabel: string
  category: BlogCategory
  cover: BlogCoverStyle
}

export function BlogIndex({
  entries,
  categories,
}: {
  entries: BlogEntry[]
  categories: readonly BlogCategory[]
}) {
  const [filter, setFilter] = useState<BlogCategory | "All">("All")
  const shown = filter === "All" ? entries : entries.filter((entry) => entry.category === filter)

  return (
    <>
      <div className={styles.filters} role="group" aria-label="Filter posts">
        {(["All", ...categories] as const).map((category) => (
          <button
            type="button"
            className={styles.filter}
            aria-pressed={filter === category}
            onClick={() => setFilter(category)}
            key={category}
          >
            {category}
          </button>
        ))}
      </div>
      <ul className={styles.cardGrid}>
        {shown.map((entry) => (
          <li key={entry.href}>
            <Link
              className={styles.card}
              href={entry.href}
              target={entry.external ? "_blank" : undefined}
              rel={entry.external ? "noopener" : undefined}
            >
              <BlogCover cover={entry.cover} category={entry.category} />
              <h2 className={styles.cardTitle}>{entry.title}</h2>
              <p className={styles.cardMeta}>{entry.dateLabel}</p>
            </Link>
          </li>
        ))}
      </ul>
    </>
  )
}
