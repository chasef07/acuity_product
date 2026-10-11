import type { StaticImageData } from "next/image"
import type { ComponentType } from "react"

import chasePortrait from "../../public/marketing/chase-fagen-v2.png"

export type BlogAuthor = {
  name: string
  role: string
  portrait: StaticImageData
  x?: string
}

export type BlogPost = {
  slug: string
  title: string
  dek: string
  description: string
  date: string
  readingTime: string
  category: string
  author: BlogAuthor
  hero?: "dead-air"
  load: () => Promise<{ default: ComponentType }>
}

const chase: BlogAuthor = {
  name: "Chase Fagen",
  role: "Co-Founder and Head of AI Engineering",
  portrait: chasePortrait,
  x: "chasef07",
}

export const blogPosts: BlogPost[] = [
  {
    slug: "name-the-slot",
    title: "Only Let the Model Handle What a Person Can Check",
    dek: "Our booking tool used to ask the model for six values. Today the slot is one argument: SEP15-0900-REYES. What that taught us about reliable tool calls.",
    description:
      "How a medical voice agent turns what a caller says into a correct booking: a 15-field signed slot token, five generations of slot references, a loaded calendar instead of a date parser, and compare-and-swap booking instead of holds.",
    date: "2026-10-14",
    readingTime: "10 min read",
    category: "Voice AI",
    author: chase,
    load: () => import("@/content/blog/name-the-slot.mdx"),
  },
  {
    slug: "the-dead-air-problem",
    title: "The Dead Air Problem",
    dek: "Our speech-to-speech voice agent said “let me check” and then never checked. Here’s how we catch that and how we fix it.",
    description:
      "How a full-duplex speech-to-speech voice agent went silent after promising to check, how a yes/no judge scorecard caught it, and how six agent teams competed in LiveKit audio simulations to fix it with a shorter prompt.",
    date: "2026-10-06",
    readingTime: "8 min read",
    category: "Voice AI",
    author: chase,
    hero: "dead-air",
    load: () => import("@/content/blog/the-dead-air-problem.mdx"),
  },
]

export function getBlogPost(slug: string) {
  return blogPosts.find((post) => post.slug === slug)
}

export function formatBlogDate(date: string) {
  return new Date(`${date}T12:00:00Z`).toLocaleDateString("en-US", {
    month: "long",
    day: "numeric",
    year: "numeric",
    timeZone: "UTC",
  })
}
