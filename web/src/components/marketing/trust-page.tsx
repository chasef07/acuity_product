import Link from "next/link"

import { siteConfig } from "@/lib/site"

import {
  MarketingFrame,
  type MarketingRoute,
} from "./enterprise-site"
import styles from "./trust-page.module.css"

type TrustSection = {
  id: string
  title: string
  paragraphs: string[]
  bullets?: string[]
}

type RelatedDocument = {
  href: MarketingRoute
  label: string
  description: string
}

export type TrustPageSpec = {
  route: MarketingRoute
  eyebrow: string
  title: string
  description: string
  introduction: string
  updated: string
  updatedIso: string
  notice?: {
    title: string
    body: string
  }
  contact: {
    title: string
    body: string
    email: string
  }
  sections: TrustSection[]
  related: RelatedDocument[]
}

function StructuredData({ spec }: { spec: TrustPageSpec }) {
  const structuredData = {
    "@context": "https://schema.org",
    "@type": "WebPage",
    "@id": `https://acuityhealth.io${spec.route}#page`,
    url: `https://acuityhealth.io${spec.route}`,
    name: spec.title,
    description: spec.description,
    dateModified: spec.updatedIso,
    publisher: { "@id": "https://acuityhealth.io/#organization" },
    isPartOf: { "@id": "https://acuityhealth.io/#website" },
    inLanguage: "en-US",
  }

  return (
    <script
      dangerouslySetInnerHTML={{
        __html: JSON.stringify(structuredData).replace(/</g, "\\u003c"),
      }}
      type="application/ld+json"
    />
  )
}

export function TrustPage({ spec }: { spec: TrustPageSpec }) {
  return (
    <MarketingFrame current={spec.route}>
      <StructuredData spec={spec} />

      <header className={styles.documentHeader}>
        <p className={styles.eyebrow}>{spec.eyebrow}</p>
        <h1>{spec.title}</h1>
        <p className={styles.introduction}>{spec.introduction}</p>
        <div className={styles.documentMeta}>
          <span>{siteConfig.legalName} d/b/a Acuity Health</span>
          <span>
            Last updated <time dateTime={spec.updatedIso}>{spec.updated}</time>
          </span>
        </div>
      </header>

      <article className={styles.document}>
        {spec.notice ? (
          <aside className={styles.notice} aria-labelledby="document-notice">
            <h2 id="document-notice">{spec.notice.title}</h2>
            <p>{spec.notice.body}</p>
          </aside>
        ) : null}

        {spec.sections.map((section) => (
          <section id={section.id} key={section.id}>
            <h2>{section.title}</h2>
            {section.paragraphs.map((paragraph) => (
              <p key={paragraph}>{paragraph}</p>
            ))}
            {section.bullets ? (
              <ul>
                {section.bullets.map((bullet) => (
                  <li key={bullet}>{bullet}</li>
                ))}
              </ul>
            ) : null}
          </section>
        ))}

        <section className={styles.contactSection}>
          <h2>{spec.contact.title}</h2>
          <p>{spec.contact.body}</p>
          <a href={`mailto:${spec.contact.email}`}>{spec.contact.email}</a>
        </section>

        <nav className={styles.relatedDocuments} aria-label="Related documents">
          <h2>Related documents</h2>
          <ul>
            {spec.related.map((item) => (
              <li key={item.href}>
                <Link href={item.href}>{item.label}</Link>
                <p>{item.description}</p>
              </li>
            ))}
          </ul>
        </nav>
      </article>
    </MarketingFrame>
  )
}
