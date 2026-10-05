import type { Metadata } from "next"
import Image from "next/image"
import Link from "next/link"
import { notFound } from "next/navigation"
import { ArrowLeft, ArrowRight } from "lucide-react"

import styles from "@/components/marketing/blog/blog.module.css"
import { DeadAirWaveform } from "@/components/marketing/blog/dead-air-waveform"
import { MarketingFrame } from "@/components/marketing/enterprise-site"
import { blogPosts, formatBlogDate, getBlogPost } from "@/lib/blog"
import { siteConfig } from "@/lib/site"

type PageProps = {
  params: Promise<{ slug: string }>
}

export const dynamicParams = false

export function generateStaticParams() {
  return blogPosts.map((post) => ({ slug: post.slug }))
}

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const post = getBlogPost((await params).slug)
  if (!post) notFound()
  const path = `/blog/${post.slug}`
  return {
    title: post.title,
    description: post.description,
    authors: [{ name: post.author.name }],
    alternates: { canonical: path },
    openGraph: {
      type: "article",
      title: post.title,
      description: post.dek,
      url: path,
      siteName: siteConfig.name,
      locale: "en_US",
      publishedTime: post.date,
      authors: [post.author.name],
    },
    twitter: {
      card: "summary_large_image",
      title: post.title,
      description: post.dek,
      creator: post.author.x ? `@${post.author.x}` : undefined,
    },
  }
}

export default async function BlogPostPage({ params }: PageProps) {
  const post = getBlogPost((await params).slug)
  if (!post) notFound()

  const { default: Content } = await post.load()
  const url = new URL(`/blog/${post.slug}`, siteConfig.url).toString()
  const articleSchema = {
    "@context": "https://schema.org",
    "@type": "TechArticle",
    headline: post.title,
    description: post.description,
    datePublished: post.date,
    url,
    author: { "@type": "Person", name: post.author.name },
    publisher: { "@id": `${siteConfig.url}/#organization` },
  }

  return (
    <MarketingFrame current="/blog">
      <div className={styles.page}>
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{
            __html: JSON.stringify(articleSchema).replace(/</g, "\\u003c"),
          }}
        />
        <header className={styles.hero}>
          <Link className={styles.backLink} href="/blog">
            <ArrowLeft size={16} aria-hidden="true" />
            Blog
          </Link>
          <p className={styles.meta}>
            <span className={styles.category}>{post.category}</span>
            <span>{formatBlogDate(post.date)}</span>
            <span aria-hidden="true">·</span>
            <span>{post.readingTime}</span>
          </p>
          <h1 className={styles.title}>{post.title}</h1>
          <p className={styles.dek}>{post.dek}</p>
          <div className={styles.byline}>
            <Image className={styles.avatar} src={post.author.portrait} alt="" width={44} height={44} />
            <span className={styles.bylineText}>
              <span className={styles.bylineName}>{post.author.name}</span>
              <span className={styles.bylineRole}>{post.author.role}</span>
            </span>
          </div>
          {post.hero === "dead-air" ? (
            <div className={styles.waveCard}>
              <DeadAirWaveform />
            </div>
          ) : null}
        </header>

        <article className={styles.article}>
          <Content />
        </article>

        <section className={styles.cta}>
          <h2>Voice AI agents for patient access, judged on every call.</h2>
          <p>
            Acuity Health helps medical enterprises onboard voice AI agents that answer calls, check
            insurance eligibility, and book appointments.
          </p>
          <Link className={styles.ctaButton} href="/work-with-us">
            Build with us
            <ArrowRight size={16} aria-hidden="true" />
          </Link>
        </section>
      </div>
    </MarketingFrame>
  )
}
