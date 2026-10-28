import { ImageResponse } from "next/og"
import { notFound } from "next/navigation"

import { blogPosts, getBlogPost } from "@/lib/blog"

export const size = { width: 1200, height: 630 }
export const contentType = "image/png"
export const alt = "Acuity Health blog"

const tones = {
  mist: { base: "#e6e5e0", glow: "#ffffff", text: "#080808", soft: "#61655f" },
  sand: { base: "#d8d2c7", glow: "#f6f3ec", text: "#080808", soft: "#61655f" },
  stone: { base: "#737770", glow: "#c4c7bf", text: "#f4f2ed", soft: "#d0d3cc" },
  dusk: { base: "#1a1b19", glow: "#646961", text: "#f4f2ed", soft: "#b7bbb3" },
  ember: { base: "#2a2521", glow: "#8a7a69", text: "#f4f2ed", soft: "#d0c7bb" },
} as const

export function generateStaticParams() {
  return blogPosts.map((post) => ({ slug: post.slug }))
}

export default async function Image({ params }: { params: Promise<{ slug: string }> }) {
  const post = getBlogPost((await params).slug)
  if (!post) notFound()
  const tone = tones[post.cover.tone]

  return new ImageResponse(
    <div
      style={{
        width: "100%",
        height: "100%",
        display: "flex",
        flexDirection: "column",
        justifyContent: "space-between",
        padding: "60px 72px",
        background: `radial-gradient(circle at 75% 20%, ${tone.glow} 0%, ${tone.base} 60%)`,
        color: tone.text,
      }}
    >
      <div style={{ display: "flex", fontSize: 24, color: tone.soft }}>Acuity Health · {post.category}</div>
      <div style={{ display: "flex", flexDirection: "column" }}>
        <div style={{ fontSize: 78, fontWeight: 300, letterSpacing: -4, lineHeight: 1.04 }}>{post.title}</div>
      </div>
      <div style={{ display: "flex", fontSize: 120, fontWeight: 300, letterSpacing: -6, lineHeight: 1, opacity: 0.28 }}>
        {post.cover.label}
      </div>
    </div>,
    size,
  )
}
