"use client"

import Link from "next/link"
import { ArrowUpRight, ChevronLeft, ChevronRight } from "lucide-react"
import { useEffect, useRef, useState } from "react"

import { StoryArt } from "./story-art"
import type { Story } from "./stories"
import styles from "./enterprise-site.module.css"

export function CaseStudies({ stories }: { stories: Story[] }) {
  const trackRef = useRef<HTMLUListElement>(null)
  const [edges, setEdges] = useState({ start: true, end: false })

  const updateEdges = () => {
    const track = trackRef.current
    if (!track) return
    setEdges({
      start: track.scrollLeft <= 1,
      end: track.scrollLeft + track.clientWidth >= track.scrollWidth - 1,
    })
  }

  useEffect(() => {
    const track = trackRef.current
    if (!track) return
    const observer = new ResizeObserver(() => {
      setEdges({
        start: track.scrollLeft <= 1,
        end: track.scrollLeft + track.clientWidth >= track.scrollWidth - 1,
      })
    })
    observer.observe(track)
    return () => observer.disconnect()
  }, [])

  const page = (direction: 1 | -1) => {
    const track = trackRef.current
    const card = track?.querySelector("li")
    if (!track || !card) return
    const gap = Number.parseFloat(getComputedStyle(track).columnGap) || 0
    const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches
    track.scrollBy({
      left: direction * (card.getBoundingClientRect().width + gap),
      behavior: reduceMotion ? "auto" : "smooth",
    })
  }

  return (
    <section className={styles.caseStudies} aria-labelledby="case-studies-title">
      <header className={styles.caseStudiesHeader}>
        <h2 id="case-studies-title">Solving the problems that move patient access.</h2>
        <div className={styles.caseStudyControls}>
          <button type="button" aria-label="Previous stories" disabled={edges.start} onClick={() => page(-1)}>
            <ChevronLeft size={18} aria-hidden="true" />
          </button>
          <button type="button" aria-label="Next stories" disabled={edges.end} onClick={() => page(1)}>
            <ChevronRight size={18} aria-hidden="true" />
          </button>
        </div>
      </header>

      <ul className={styles.caseStudyTrack} ref={trackRef} onScroll={updateEdges}>
        {stories.map((story) => {
          const content = (
            <>
              <StoryArt kind={story.art} />
              <span className={styles.caseStudyTitle}>{story.title}</span>
              <ArrowUpRight className={styles.caseStudyArrow} size={28} strokeWidth={1.5} aria-hidden="true" />
              <span className={styles.caseStudyPartner}>{story.partner}</span>
            </>
          )
          return (
            <li key={story.partner}>
              {story.external ? (
                <a className={styles.caseStudyCard} data-tone={story.tone} href={story.href} target="_blank" rel="noopener">
                  {content}
                </a>
              ) : (
                <Link className={styles.caseStudyCard} data-tone={story.tone} href={story.href}>
                  {content}
                </Link>
              )}
            </li>
          )
        })}
      </ul>
    </section>
  )
}
