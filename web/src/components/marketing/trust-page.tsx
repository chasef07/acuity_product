import Link from "next/link"
import { ArrowRight, Check, FileText, ShieldCheck } from "lucide-react"

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

      <header className={styles.hero}>
        <div className={styles.heroCopy}>
          <p className={styles.eyebrow}>{spec.eyebrow}</p>
          <h1>{spec.title}</h1>
          <p className={styles.heroDescription}>{spec.introduction}</p>
        </div>

        <aside className={styles.documentCard} aria-label="Document details">
          <div className={styles.documentIcon}>
            {spec.route === "/security" ? (
              <ShieldCheck aria-hidden="true" size={20} />
            ) : (
              <FileText aria-hidden="true" size={20} />
            )}
          </div>
          <p>Public trust document</p>
          <dl>
            <div>
              <dt>Operating entity</dt>
              <dd>{siteConfig.legalName}</dd>
            </div>
            <div>
              <dt>Brand</dt>
              <dd>Acuity Health</dd>
            </div>
            <div>
              <dt>Last updated</dt>
              <dd>
                <time dateTime={spec.updatedIso}>{spec.updated}</time>
              </dd>
            </div>
          </dl>
        </aside>
      </header>

      {spec.notice ? (
        <section className={styles.notice} aria-labelledby="document-notice">
          <ShieldCheck aria-hidden="true" size={24} />
          <div>
            <h2 id="document-notice">{spec.notice.title}</h2>
            <p>{spec.notice.body}</p>
          </div>
        </section>
      ) : null}

      <div className={styles.documentLayout}>
        <nav className={styles.contents} aria-label="On this page">
          <p>On this page</p>
          <ol>
            {spec.sections.map((section, index) => (
              <li key={section.id}>
                <span>{String(index + 1).padStart(2, "0")}</span>
                <a href={`#${section.id}`}>{section.title}</a>
              </li>
            ))}
          </ol>
        </nav>

        <article className={styles.documentBody}>
          {spec.sections.map((section, index) => (
            <section id={section.id} key={section.id}>
              <span>{String(index + 1).padStart(2, "0")}</span>
              <h2>{section.title}</h2>
              {section.paragraphs.map((paragraph) => (
                <p key={paragraph}>{paragraph}</p>
              ))}
              {section.bullets ? (
                <ul>
                  {section.bullets.map((bullet) => (
                    <li key={bullet}>
                      <Check aria-hidden="true" size={17} />
                      <span>{bullet}</span>
                    </li>
                  ))}
                </ul>
              ) : null}
            </section>
          ))}

          <section className={styles.contactSection}>
            <span>Contact</span>
            <h2>{spec.contact.title}</h2>
            <p>{spec.contact.body}</p>
            <a href={`mailto:${spec.contact.email}`}>{spec.contact.email}</a>
          </section>
        </article>
      </div>

      <section className={styles.relatedSection}>
        <div>
          <p className={styles.eyebrow}>Related documents</p>
          <h2>Continue the trust review.</h2>
        </div>
        <div className={styles.relatedGrid}>
          {spec.related.map((item) => (
            <Link href={item.href} key={item.href}>
              <span>{item.label}</span>
              <p>{item.description}</p>
              <ArrowRight aria-hidden="true" size={17} />
            </Link>
          ))}
        </div>
      </section>
    </MarketingFrame>
  )
}
