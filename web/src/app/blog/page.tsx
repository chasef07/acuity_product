import Link from "next/link"

import styles from "@/components/marketing/blog/blog.module.css"
import { DeadAirWaveform } from "@/components/marketing/blog/dead-air-waveform"
import { SlotTokenArt } from "@/components/marketing/blog/slot-token-art"
import { MarketingFrame } from "@/components/marketing/enterprise-site"
import { blogPosts, formatBlogDate } from "@/lib/blog"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/blog",
  title: "Blog",
  description:
    "Field notes from building speech-to-speech voice AI for medicine: evals, judges, simulations, and what breaks in production.",
})

export default function BlogIndexPage() {
  const posts = [...blogPosts].sort((a, b) => b.date.localeCompare(a.date))

  return (
    <MarketingFrame current="/blog">
      <div className={styles.page}>
        <section className={styles.index}>
          <header>
            <h1>Blog</h1>
            <p>Field notes from building voice AI for medicine.</p>
          </header>
          <ul className={styles.cardGrid}>
            {posts.map((post) => (
              <li key={post.slug}>
                <Link className={styles.card} href={`/blog/${post.slug}`}>
                  <div className={styles.cardArt}>
                    {post.hero === "dead-air" ? <DeadAirWaveform compact /> : <SlotTokenArt />}
                  </div>
                  <h2 className={styles.cardTitle}>{post.title}</h2>
                  <p className={styles.cardMeta}>{formatBlogDate(post.date)}</p>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      </div>
    </MarketingFrame>
  )
}
