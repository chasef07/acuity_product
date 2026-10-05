import { ImageResponse } from "next/og"
import { notFound } from "next/navigation"

import { agentBars, callerBars } from "@/components/marketing/blog/dead-air-waveform"
import { blogPosts, getBlogPost } from "@/lib/blog"

export const size = { width: 1200, height: 630 }
export const contentType = "image/png"
export const alt = "Acuity Health blog"

export function generateStaticParams() {
  return blogPosts.map((post) => ({ slug: post.slug }))
}

function Bar({ height, width, opacity = 1 }: { height: number; width: number; opacity?: number }) {
  return (
    <div
      style={{
        width,
        height: height * 1.5,
        marginRight: width,
        borderRadius: width,
        background: "#f4f2ed",
        opacity,
      }}
    />
  )
}

export default async function Image({ params }: { params: Promise<{ slug: string }> }) {
  const post = getBlogPost((await params).slug)
  if (!post) notFound()

  return new ImageResponse(
    <div
      style={{
        width: "100%",
        height: "100%",
        display: "flex",
        flexDirection: "column",
        justifyContent: "space-between",
        padding: "64px 72px",
        background: "radial-gradient(circle at 50% -30%, #2a2b28 0%, #080808 62%)",
        color: "#f4f2ed",
      }}
    >
      <div style={{ display: "flex", fontSize: 24, color: "#b7bbb3" }}>Acuity Health · Blog</div>
      <div style={{ display: "flex", flexDirection: "column" }}>
        <div style={{ fontSize: 104, fontWeight: 300, letterSpacing: -5, lineHeight: 1 }}>{post.title}</div>
        <div style={{ marginTop: 26, maxWidth: 980, fontSize: 30, lineHeight: 1.35, color: "#b7bbb3" }}>
          {post.dek}
        </div>
      </div>
      <div style={{ display: "flex", alignItems: "center", height: 100 }}>
        {post.hero === "dead-air" ? (
          <>
            {agentBars.map((height, index) => (
              <Bar key={`agent-${index}`} height={height} width={5} opacity={0.25 + (index / agentBars.length) * 0.6} />
            ))}
            <div style={{ display: "flex", flexDirection: "column", alignItems: "center", width: 350, margin: "0 18px" }}>
              <div style={{ fontSize: 18, letterSpacing: 4, color: "#d9733f", textTransform: "uppercase" }}>
                dead air
              </div>
              <div style={{ width: "100%", height: 3, marginTop: 10, borderRadius: 2, background: "#d9733f" }} />
            </div>
            {callerBars.map((height, index) => (
              <Bar key={`caller-${index}`} height={height} width={6} />
            ))}
          </>
        ) : null}
      </div>
    </div>,
    size,
  )
}
