import Link from "next/link"
import { ArrowRight } from "lucide-react"

import styles from "@/components/marketing/blog/blog.module.css"
import { DeadAirWaveform } from "@/components/marketing/blog/dead-air-waveform"
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
          <header className={styles.indexHeader}>
            <h1>Blog</h1>
            <p>Field notes from building voice AI for medicine.</p>
          </header>
          <ul className={styles.cardGrid}>
            {posts.map((post) => (
              <li key={post.slug}>
                <Link className={styles.card} href={`/blog/${post.slug}`}>
                  {post.hero === "dead-air" ? (
                    <div className={styles.cardArt}>
                      <DeadAirWaveform compact />
                    </div>
                  ) : null}
                  <div className={styles.cardBody}>
                    <p className={styles.cardMeta}>
                      {post.category} · {formatBlogDate(post.date)}
                    </p>
                    <h2 className={styles.cardTitle}>{post.title}</h2>
                    <p className={styles.cardDek}>{post.dek}</p>
                    <span className={styles.cardFooter}>
                      {post.readingTime}
                      <ArrowRight size={16} aria-hidden="true" />
                    </span>
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      </div>
    </MarketingFrame>
  )
}
