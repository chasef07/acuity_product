import Link from "next/link"
import { ArrowRight, Check } from "lucide-react"

import {
  MarketingFrame,
  type MarketingRoute,
} from "./enterprise-site"
import styles from "./commercial-page.module.css"

type TraceStep = {
  label: string
  title: string
  description: string
}

type DetailSection = {
  eyebrow: string
  title: string
  description: string
  bullets: string[]
}

type ComparisonRow = {
  label: string
  first: string
  second: string
}

type RelatedPage = {
  href: MarketingRoute
  label: string
  description: string
}

export type CommercialPageSpec = {
  route: MarketingRoute
  schemaType: "Service" | "WebPage"
  schemaName: string
  eyebrow: string
  title: string
  description: string
  trace: {
    eyebrow: string
    title: string
    steps: TraceStep[]
  }
  proof?: {
    eyebrow: string
    title: string
    description: string
    metrics: ReadonlyArray<{ value: string; label: string }>
  }
  details: DetailSection[]
  comparison?: {
    eyebrow: string
    title: string
    firstColumn: string
    secondColumn: string
    rows: ComparisonRow[]
  }
  validation?: {
    eyebrow: string
    title: string
    description: string
    href: `https://${string}`
    label: string
  }
  questions: Array<{ question: string; answer: string }>
  related: RelatedPage[]
}

function StructuredData({ spec }: { spec: CommercialPageSpec }) {
  const structuredData = {
    "@context": "https://schema.org",
    "@type": spec.schemaType,
    "@id": `https://acuityhealth.io${spec.route}#page`,
    url: `https://acuityhealth.io${spec.route}`,
    name: spec.schemaName,
    description: spec.description,
    provider:
      spec.schemaType === "Service"
        ? { "@id": "https://acuityhealth.io/#organization" }
        : undefined,
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

export function CommercialPage({ spec }: { spec: CommercialPageSpec }) {
  return (
    <MarketingFrame current={spec.route}>
      <StructuredData spec={spec} />

      <section className={styles.hero}>
        <div className={styles.heroCopy}>
          <p className={styles.eyebrow}>{spec.eyebrow}</p>
          <h1>{spec.title}</h1>
          <p className={styles.heroDescription}>{spec.description}</p>
          <div className={styles.heroActions}>
            <Link className={styles.primaryAction} href="/work-with-us">
              Map the workflow <ArrowRight aria-hidden="true" size={17} />
            </Link>
            <Link className={styles.secondaryAction} href="/method">
              See how Acuity deploys
            </Link>
          </div>
        </div>

        <aside className={styles.heroArtifact} aria-label="Acuity operating trace">
          <span className={styles.artifactLabel}>Operational trace</span>
          {spec.trace.steps.map((step, index) => (
            <div className={styles.artifactRow} key={step.label}>
              <span>{String(index + 1).padStart(2, "0")}</span>
              <div>
                <strong>{step.label}</strong>
                <small>{step.title}</small>
              </div>
            </div>
          ))}
          <div className={styles.artifactReceipt}>
            <Check aria-hidden="true" size={16} />
            Outcome and owner recorded
          </div>
        </aside>
      </section>

      <section className={styles.traceSection}>
        <header>
          <p className={styles.eyebrow}>{spec.trace.eyebrow}</p>
          <h2>{spec.trace.title}</h2>
        </header>
        <div className={styles.traceGrid}>
          {spec.trace.steps.map((step, index) => (
            <article key={step.label}>
              <span>{String(index + 1).padStart(2, "0")}</span>
              <p>{step.label}</p>
              <h3>{step.title}</h3>
              <small>{step.description}</small>
            </article>
          ))}
        </div>
      </section>

      {spec.proof ? (
        <section className={styles.proofSection}>
          <div>
            <p className={styles.eyebrow}>{spec.proof.eyebrow}</p>
            <h2>{spec.proof.title}</h2>
            <p>{spec.proof.description}</p>
          </div>
          <dl>
            {spec.proof.metrics.map((metric) => (
              <div key={metric.label}>
                <dt>{metric.value}</dt>
                <dd>{metric.label}</dd>
              </div>
            ))}
          </dl>
        </section>
      ) : null}

      <section className={styles.detailSection}>
        {spec.details.map((detail) => (
          <article key={detail.title}>
            <div>
              <p className={styles.eyebrow}>{detail.eyebrow}</p>
              <h2>{detail.title}</h2>
              <p>{detail.description}</p>
            </div>
            <ul>
              {detail.bullets.map((bullet) => (
                <li key={bullet}>
                  <Check aria-hidden="true" size={17} />
                  {bullet}
                </li>
              ))}
            </ul>
          </article>
        ))}
      </section>

      {spec.comparison ? (
        <section className={styles.comparisonSection}>
          <header>
            <p className={styles.eyebrow}>{spec.comparison.eyebrow}</p>
            <h2>{spec.comparison.title}</h2>
          </header>
          <div
            className={styles.comparisonTable}
            data-testid="comparison-table"
            role="table"
          >
            <div className={styles.comparisonHeader} role="row">
              <span role="columnheader">Decision</span>
              <span role="columnheader">{spec.comparison.firstColumn}</span>
              <span role="columnheader">{spec.comparison.secondColumn}</span>
            </div>
            {spec.comparison.rows.map((row) => (
              <div className={styles.comparisonRow} role="row" key={row.label}>
                <strong role="rowheader">{row.label}</strong>
                <span role="cell">{row.first}</span>
                <span role="cell">{row.second}</span>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      {spec.validation ? (
        <section className={styles.relatedSection}>
          <div>
            <p className={styles.eyebrow}>{spec.validation.eyebrow}</p>
            <h2>{spec.validation.title}</h2>
          </div>
          <div className={styles.relatedGrid}>
            <a
              href={spec.validation.href}
              rel="noreferrer"
              target="_blank"
            >
              <span>{spec.validation.label}</span>
              <p>{spec.validation.description}</p>
              <ArrowRight aria-hidden="true" size={17} />
            </a>
          </div>
        </section>
      ) : null}

      <section className={styles.questionsSection}>
        <header>
          <p className={styles.eyebrow}>Decision questions</p>
          <h2>What teams ask before changing patient access.</h2>
        </header>
        <div>
          {spec.questions.map((item) => (
            <details key={item.question}>
              <summary>{item.question}</summary>
              <p>{item.answer}</p>
            </details>
          ))}
        </div>
      </section>

      <section className={styles.relatedSection}>
        <div>
          <p className={styles.eyebrow}>Continue the evaluation</p>
          <h2>Follow the evidence, not the category label.</h2>
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

      <section className={styles.finalCta}>
        <div>
          <p className={styles.eyebrow}>Start with one workflow</p>
          <h2>Bring us the patient-access operation that needs to change.</h2>
          <p>
            We’ll baseline the failure modes, map the rules and handoffs, and
            define the evidence required before anything scales.
          </p>
        </div>
        <Link className={styles.lightAction} href="/work-with-us">
          Request a working session <ArrowRight aria-hidden="true" size={17} />
        </Link>
      </section>
    </MarketingFrame>
  )
}
