import Link from "next/link"

import { BlogCover } from "@/components/marketing/blog/blog-cover"
import { BlogIndex, type BlogEntry } from "@/components/marketing/blog/blog-index"
import styles from "@/components/marketing/blog/blog.module.css"
import { MarketingFrame } from "@/components/marketing/enterprise-site"
import { liveKitCaseStudyUrl } from "@/components/marketing/stories"
import { blogCategories, blogPosts, formatBlogDate } from "@/lib/blog"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/blog",
  title: "Blog",
  description:
    "Stories, announcements, and product updates from Acuity Health on building AI agents for medical practices.",
})

const liveKitStory = {
  href: liveKitCaseStudyUrl,
  external: true,
  title: "How Acuity Health transforms patient access with LiveKit voice agents",
  date: "2026-10-21",
  category: "Customer stories",
  cover: { label: "LiveKit", tone: "stone", shape: "a", art: "wave" },
} as const

export default function BlogIndexPage() {
  const posts = [...blogPosts].sort((a, b) => b.date.localeCompare(a.date))
  const [featured] = posts
  const entries: BlogEntry[] = [
    ...posts.map((post) => ({
      href: `/blog/${post.slug}`,
      external: false,
      title: post.title,
      date: post.date,
      category: post.category,
      cover: post.cover,
    })),
    liveKitStory,
  ]
    .sort((a, b) => b.date.localeCompare(a.date))
    .map((entry) => ({ ...entry, dateLabel: formatBlogDate(entry.date) }))

  return (
    <MarketingFrame current="/blog">
      <div className={styles.page}>
        <section className={styles.index}>
          <Link className={styles.featured} href={`/blog/${featured.slug}`}>
            <BlogCover cover={featured.cover} category={featured.category} className={styles.featuredCover} />
            <span className={styles.featuredText}>
              <span className={styles.cardMeta}>{formatBlogDate(featured.date)}</span>
              <span className={styles.featuredTitle}>{featured.title}</span>
              <span className={styles.featuredDek}>{featured.dek}</span>
              <span className={styles.featuredCta}>Read the article</span>
            </span>
          </Link>

          <header>
            <h1>All posts</h1>
            <p>Stories, announcements, and product updates.</p>
          </header>
          <BlogIndex entries={entries} categories={blogCategories} />
        </section>
      </div>
    </MarketingFrame>
  )
}
