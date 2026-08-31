import type { ReactNode } from "react"
import type { StaticImageData } from "next/image"
import Image from "next/image"
import Link from "next/link"
import { ArrowRight, Check } from "lucide-react"
import { Geist, JetBrains_Mono, Newsreader } from "next/font/google"

import { AcuityMark } from "@/components/acuity-mark"
import {
  PortalSignInTrigger,
  SignInDialog,
} from "@/components/auth/sign-in-dialog"
import { cn } from "@/lib/utils"

import { PixelWaveField } from "./pixel-wave-field"
import { WorkWithUsForm } from "./work-with-us-form"
import styles from "./enterprise-site.module.css"

import chasePortrait from "../../../public/marketing/chase-fagen-v2.png"
import kylePortrait from "../../../public/marketing/kyle-shechtman-2026.png"

const sans = Geist({
  subsets: ["latin"],
  variable: "--font-acuity-sans",
})

const display = Newsreader({
  subsets: ["latin"],
  variable: "--font-acuity-display",
})

const mono = JetBrains_Mono({
  subsets: ["latin"],
  variable: "--font-jetbrains-mono",
})

export type MarketingRoute =
  | "/"
  | "/method"
  | "/who-we-are"
  | "/work-with-us"
  | "/privacy-policy"
  | "/terms-of-service"
  | "/security"

const navigation: Array<{ href: MarketingRoute; label: string }> = [
  { href: "/", label: "Home" },
  { href: "/method", label: "Our Method" },
  { href: "/who-we-are", label: "Who We Are" },
]

const footerNavigation = [
  {
    label: "Product",
    links: [
      { href: "/method", label: "The Acuity Health Method" },
    ],
  },
  {
    label: "Company",
    links: [
      { href: "/who-we-are", label: "Who We Are" },
      { href: "/work-with-us", label: "Work With Us" },
      {
        href: "https://www.linkedin.com/company/acuityhealth/",
        label: "LinkedIn",
        external: true,
      },
    ],
  },
  {
    label: "Resources",
    links: [
      { href: "/sign-in", label: "Sign in" },
      { href: "/security", label: "Security, Privacy & HIPAA" },
      { href: "/privacy-policy", label: "Privacy Policy" },
      { href: "/terms-of-service", label: "Terms of Service" },
    ],
  },
] as const

function SiteHeader({ current }: { current: MarketingRoute }) {
  return (
    <header className={styles.siteHeader}>
      <Link className={styles.brand} href="/" aria-label="Acuity Health home">
        <AcuityMark className={styles.brandMark} />
        <span>Acuity Health</span>
      </Link>

      <nav className={styles.primaryNav} aria-label="Main navigation">
        {navigation.map((item) => (
          <Link
            className={styles.navLink}
            href={item.href}
            aria-current={current === item.href ? "page" : undefined}
            key={item.href}
          >
            {item.label}
          </Link>
        ))}
      </nav>

      <div className={styles.headerActions}>
        <PortalSignInTrigger className={styles.portalLink} />
        <Link className={styles.workLink} href="/work-with-us">
          Work With Us
        </Link>
      </div>
    </header>
  )
}

function SiteFooter() {
  return (
    <footer className={styles.siteFooter}>
      <div className={styles.footerPrimary}>
        <nav className={styles.footerNavigation} aria-label="Footer navigation">
          {footerNavigation.map((section) => (
            <section className={styles.footerGroup} key={section.label}>
              <h2>{section.label}</h2>
              <ul>
                {section.links.map((link) => (
                  <li key={link.href}>
                    {"external" in link ? (
                      <a href={link.href} rel="noreferrer" target="_blank">
                        {link.label}
                      </a>
                    ) : (
                      <Link href={link.href}>{link.label}</Link>
                    )}
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </nav>
      </div>

      <div className={styles.footerClose}>
        <Link className={styles.footerWordmark} href="/" aria-label="Acuity Health home">
          <AcuityMark className={styles.footerWordmarkMark} />
          <span>Acuity Health</span>
        </Link>
      </div>
    </footer>
  )
}

export function MarketingFrame({
  children,
  current,
  initiallyOpen = false,
}: {
  children: ReactNode
  current: MarketingRoute
  initiallyOpen?: boolean
}) {
  return (
    <SignInDialog
      key={initiallyOpen ? "sign-in-open" : "sign-in-closed"}
      initiallyOpen={initiallyOpen}
    >
      <div className={cn(styles.site, sans.variable, display.variable, mono.variable)}>
        <a className={styles.skipLink} href="#main-content">
          Skip to main content
        </a>
        <SiteHeader current={current} />
        <main id="main-content">{children}</main>
        <SiteFooter />
      </div>
    </SignInDialog>
  )
}

function Waveform({ compact = false }: { compact?: boolean }) {
  return (
    <span
      className={cn(styles.waveform, compact && styles.waveformCompact)}
      aria-hidden="true"
    >
      {[18, 34, 54, 42, 66, 48, 28, 62, 46, 34, 20].map((height, index) => (
        <i style={{ height: `${height}%` }} key={`${height}-${index}`} />
      ))}
    </span>
  )
}

export function MethodVenn() {
  return (
    <div className={styles.venn} data-testid="method-venn">
      <div className={cn(styles.vennCircle, styles.vennSystem)}>
        <div className={styles.vennCopy}>
          <p>Agentic<br /><span>system design</span></p>
          <ul>
            <li>Medical AI agents</li>
            <li>Evals + safeguards</li>
            <li>Enterprise integrations</li>
          </ul>
        </div>
      </div>
      <div className={cn(styles.vennCircle, styles.vennTransformation)}>
        <div className={styles.vennCopy}>
          <p>Workflow<br />transformation</p>
          <ul>
            <li>Change management</li>
            <li>Workflow redesign</li>
            <li>Frontline training</li>
          </ul>
        </div>
      </div>
      <div className={styles.vennCenter}>
        <strong>The Acuity Health Method</strong>
      </div>
    </div>
  )
}

function PartnershipCta() {
  return (
    <section className={styles.partnershipCta}>
      <div>
        <p className={styles.eyebrow}>Work with us</p>
        <h2>Bring us the operation that needs to change.</h2>
        <p>
          We’ll work beside your team from the first diagnostic through rollout.
        </p>
      </div>
      <Link className={styles.lightButton} href="/work-with-us">
        Start the conversation <ArrowRight size={17} aria-hidden="true" />
      </Link>
    </section>
  )
}

export function EnterpriseHome({
  initiallyOpen = false,
}: {
  initiallyOpen?: boolean
}) {
  return (
    <MarketingFrame current="/" initiallyOpen={initiallyOpen}>
      <section className={styles.hero}>
        <div className={styles.heroCopy}>
          <h1>Redesign patient access with medical AI agents.</h1>
          <p className={styles.heroBody}>
            Acuity answers calls, completes approved work in the systems your
            team already uses, and brings staff in when judgment or ownership is
            required. Then we stay until the new operating model works.
          </p>
          <div className={styles.heroActions}>
            <Link className={styles.darkButton} href="/work-with-us">
              Work with us
            </Link>
            <Link className={styles.textLink} href="/method">
              See the Acuity Health Method
            </Link>
          </div>
        </div>
        <PixelWaveField />
      </section>

      <section className={styles.methodPageVenn}>
        <header className={styles.sectionHeader}>
          <div>
            <p className={styles.eyebrow}>The Acuity Health Method</p>
            <h2>Two capabilities make enterprise AI work.</h2>
          </div>
          <p>
            Acuity combines medical voice system design with the workflow expertise
            needed to deploy it across the enterprise.
          </p>
        </header>
        <MethodVenn />
      </section>

      <section className={styles.storyPreview}>
        <div className={styles.storyArtifact} aria-hidden="true">
          <Waveform compact />
          <span>Consulting became the product.</span>
        </div>
        <div>
          <p className={styles.eyebrow}>The founding story</p>
          <h2>We started close to the work.</h2>
          <p>
            Consulting taught us the hard part was not answering the phone. It
            was understanding the workflow and staying long enough to redesign
            it with the people who run it.
          </p>
          <Link className={styles.inlineArrowLink} href="/who-we-are">
            Read our story <ArrowRight size={16} aria-hidden="true" />
          </Link>
        </div>
      </section>

      <PartnershipCta />
    </MarketingFrame>
  )
}

export function MethodPageContent() {
  return (
    <MarketingFrame current="/method">
      <section className={styles.methodPageVenn}>
        <header className={styles.sectionHeader}>
          <div>
            <p className={styles.eyebrow}>The Acuity Health Method</p>
            <h1>Two capabilities make enterprise AI work.</h1>
          </div>
          <p>
            Acuity Health combines medical voice system design with the workflow
            expertise needed to deploy it across the enterprise.
          </p>
        </header>
        <MethodVenn />
      </section>

      <section className={styles.commitmentSection}>
        <div>
          <p className={styles.eyebrow}>The deployment commitment</p>
          <h2>Prove it in the operation. Then earn the right to scale.</h2>
        </div>
        <ul>
          <li><Check size={17} aria-hidden="true" /> Founders remain involved from diagnosis through executive review.</li>
          <li><Check size={17} aria-hidden="true" /> We go on-site to understand frontline reality and support launch.</li>
          <li><Check size={17} aria-hidden="true" /> Expansion waits for performance, handoffs, adoption, and ownership.</li>
          <li><Check size={17} aria-hidden="true" /> The customer gains capability at every stage.</li>
        </ul>
      </section>

      <PartnershipCta />
    </MarketingFrame>
  )
}

function FounderPanel({
  fullName,
  role,
  linkedinHref,
  location,
  imageSrc,
  portraitClassName,
  children,
}: {
  fullName: string
  role: string
  linkedinHref: `https://${string}`
  location: string
  imageSrc: StaticImageData
  portraitClassName?: string
  children: ReactNode
}) {
  return (
    <article className={styles.founderPanel}>
      <div className={cn(styles.founderPortrait, portraitClassName)}>
        <Image
          alt={`${fullName}, Acuity Health co-founder`}
          fill
          placeholder="blur"
          sizes="(max-width: 860px) calc(100vw - 48px), 46vw"
          src={imageSrc}
        />
      </div>
      <div className={styles.founderIdentity}>
        <p className={styles.eyebrow}>Co-founder</p>
        <h2>{fullName}</h2>
        <div className={styles.founderProfile}>
          <p>{role}</p>
          <a href={linkedinHref} rel="noreferrer" target="_blank">
            LinkedIn <ArrowRight aria-hidden="true" size={14} />
          </a>
        </div>
      </div>
      <details className={styles.founderThought} open>
        <summary>Currently on my mind</summary>
        <div>
          {children}
          <span>{location}</span>
        </div>
      </details>
    </article>
  )
}

export function WhoWeArePageContent() {
  return (
    <MarketingFrame current="/who-we-are">
      <script
        dangerouslySetInnerHTML={{
          __html: JSON.stringify({
            "@context": "https://schema.org",
            "@graph": [
              {
                "@type": "Person",
                "@id": "https://acuityhealth.io/who-we-are#kyle-shechtman",
                name: "Kyle Shechtman",
                jobTitle: "Co-founder and CEO",
                sameAs: ["https://www.linkedin.com/in/kyle-shechtman"],
                worksFor: { "@id": "https://acuityhealth.io/#organization" },
              },
              {
                "@type": "Person",
                "@id": "https://acuityhealth.io/who-we-are#chase-fagen",
                name: "Chase Fagen",
                jobTitle: "Co-founder",
                sameAs: ["https://www.linkedin.com/in/chase-fagen-198947180"],
                worksFor: { "@id": "https://acuityhealth.io/#organization" },
              },
            ],
          }).replace(/</g, "\\u003c"),
        }}
        type="application/ld+json"
      />
      <section className={styles.foundingStory}>
        <p className={styles.eyebrow}>The founding story</p>
        <div>
          <h1>Acuity Health began close to the patient-access work.</h1>
          <div className={styles.storyColumns}>
            <p>
              Working closely with medical practices taught us that the hard part
              was never simply teaching a model to answer the phone. It was
              understanding how the practice actually worked: how scheduling rules
              changed by location, where handoffs failed, which exceptions required
              judgment, and what staff and patients needed to trust the system.
              Making the technology useful required workflow redesign, frontline
              change management, and a new operating model that people could
              actually adopt.
            </p>
            <p>
              That understanding cannot be handed from a sales team to an
              implementation queue. So we built Acuity Health the same way we learned to
              solve the problem: founder-deployed, inside the operation, with the
              relationship treated as part of the product.
            </p>
          </div>
        </div>
      </section>

      <section className={styles.foundersSection}>
        <header><p className={styles.eyebrow}>Meet the founders</p><h2>The same people stay in the work.</h2></header>
        <div className={styles.founderGrid}>
          <FounderPanel
            fullName="Kyle Shechtman"
            imageSrc={kylePortrait}
            linkedinHref="https://www.linkedin.com/in/kyle-shechtman"
            location="August 2026"
            portraitClassName={styles.founderPortraitKyle}
            role="Co-founder & CEO"
          >
            <p>
              Enterprise AI requires a forward-deployed approach because the real
              specification lives inside the operation. The team has to understand
              the workflow, earn trust, and stay long enough for the change to stick.
            </p>
          </FounderPanel>
          <FounderPanel
            fullName="Chase Fagen"
            imageSrc={chasePortrait}
            linkedinHref="https://www.linkedin.com/in/chase-fagen-198947180"
            location="August 2026"
            portraitClassName={styles.founderPortraitChase}
            role="Co-founder"
          >
            <p>
              When intelligence has negligible marginal cost and is abundant,
              where does value creation accrue? Who owns the interface through
              which intelligence enters the economy?
            </p>
          </FounderPanel>
        </div>
      </section>

      <section
        aria-labelledby="company-mission"
        className={styles.missionVision}
      >
        <div className={styles.missionStatement}>
          <p className={styles.eyebrow}>Our mission</p>
          <h2 id="company-mission">
            Free medical practices from administrative overload so every patient
            can be treated like a VIP.
          </h2>
        </div>
        <div className={styles.visionStatement}>
          <p className={styles.eyebrow}>Our vision</p>
          <p>
            A future where AI runs the administration, humans elevate the care,
            and no patient falls through the cracks.
          </p>
        </div>
      </section>

      <section className={styles.principlesSection}>
        <p className={styles.eyebrow}>How we build</p>
        <div>
          <article>
            <div>
              <h3>Radical simplicity</h3>
              <small>KANSO · 簡素</small>
            </div>
            <p>We solve the real problem with fewer moving parts.</p>
          </article>
          <article>
            <div>
              <h3>Continuous improvement</h3>
              <small>KAIZEN · 改善</small>
            </div>
            <p>
              We surface problems, learn from them, and strengthen the system.
            </p>
          </article>
          <article>
            <div>
              <h3>Craft</h3>
              <small>MONOZUKURI · ものづくり</small>
            </div>
            <p>
              We make every interaction, handoff, and outcome precise and
              dependable.
            </p>
          </article>
        </div>
      </section>

      <PartnershipCta />
    </MarketingFrame>
  )
}

export function WorkWithUsPageContent() {
  return (
    <MarketingFrame current="/work-with-us">
      <section className={styles.inquirySection} id="conversation">
        <div className={styles.inquiryIntro}>
          <p className={styles.eyebrow}>Request a working session</p>
          <h1>Let’s redesign one patient-access workflow together.</h1>
        </div>
        <WorkWithUsForm />
      </section>

      <section className={styles.collaborationSection}>
        <header>
          <p className={styles.eyebrow}>How we work together</p>
        </header>
        <div className={styles.collaborationSteps}>
          <article>
            <span>01</span>
            <h3>Baseline</h3>
            <p>Agree on volumes, failure modes, handoffs, and the KPIs that matter.</p>
          </article>
          <article>
            <span>02</span>
            <h3>Redesign</h3>
            <p>Map the rules, exceptions, ownership, and new workflow with the people who run it.</p>
          </article>
          <article>
            <span>03</span>
            <h3>Test</h3>
            <p>Launch a bounded pilot that tests the highest-risk assumptions in real operations.</p>
          </article>
          <article>
            <span>04</span>
            <h3>Prove</h3>
            <p>Compare results with the agreed KPIs, strengthen weak points, and expand when the evidence holds.</p>
          </article>
        </div>
      </section>
    </MarketingFrame>
  )
}
