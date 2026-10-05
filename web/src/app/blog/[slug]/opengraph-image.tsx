import { ImageResponse } from "next/og"

import { blogPosts, getBlogPost } from "@/lib/blog"

export const size = { width: 1200, height: 630 }
export const contentType = "image/png"
export const alt = "Acuity Health blog"

export function generateStaticParams() {
  return blogPosts.map((post) => ({ slug: post.slug }))
}

const agentBars = Array.from({ length: 34 }, (_, i) =>
  Math.round(14 + 70 * Math.abs(Math.sin(i * 1.7) * Math.cos(i * 0.45))),
)

const callerBars = Array.from({ length: 12 }, (_, i) =>
  Math.round(12 + 50 * Math.abs(Math.sin(i * 2.3 + 1) * Math.cos(i * 0.9))),
)

export default async function Image({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params
  const post = getBlogPost(slug)
  const title = post?.title ?? "Acuity Health Blog"
  const dek = post?.dek ?? ""

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
      <div style={{ display: "flex", alignItems: "center", fontSize: 24, color: "#b7bbb3" }}>
        Acuity Health · Blog
      </div>
      <div style={{ display: "flex", flexDirection: "column" }}>
        <div style={{ fontSize: 104, fontWeight: 300, letterSpacing: -5, lineHeight: 1 }}>{title}</div>
        <div style={{ marginTop: 26, maxWidth: 980, fontSize: 30, lineHeight: 1.35, color: "#b7bbb3" }}>
          {dek}
        </div>
      </div>
      {post?.hero === "dead-air" ? (
        <div style={{ display: "flex", alignItems: "center", height: 100 }}>
          {agentBars.map((height, index) => (
            <div
              key={`agent-${index}`}
              style={{
                width: 6,
                height,
                marginRight: 6,
                borderRadius: 3,
                background: "#f4f2ed",
                opacity: 0.25 + (index / agentBars.length) * 0.6,
              }}
            />
          ))}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              width: 430,
              height: 100,
              margin: "0 18px",
              position: "relative",
            }}
          >
            <div style={{ width: "100%", height: 3, borderRadius: 2, background: "#d9733f" }} />
            <div
              style={{
                position: "absolute",
                left: 158,
                top: 8,
                fontSize: 18,
                letterSpacing: 4,
                color: "#d9733f",
                textTransform: "uppercase",
              }}
            >
              dead air
            </div>
          </div>
          {callerBars.map((height, index) => (
            <div
              key={`caller-${index}`}
              style={{ width: 7, height, marginRight: 7, borderRadius: 4, background: "#f4f2ed" }}
            />
          ))}
        </div>
      ) : (
        <div style={{ display: "flex" }} />
      )}
    </div>,
    size,
  )
}
