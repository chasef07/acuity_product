import type { ReactNode } from "react"
import type { StaticImageData } from "next/image"
import Image from "next/image"
import Link from "next/link"
import { ArrowRight, Check } from "lucide-react"
import { Geist, Newsreader } from "next/font/google"

import { AcuityMark } from "@/components/acuity-mark"
import {
  PortalSignInTrigger,
  SignInDialog,
} from "@/components/auth/sign-in-dialog"
import { cn } from "@/lib/utils"

import { PixelWaveField } from "./pixel-wave-field"
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

type MarketingRoute = "/" | "/method" | "/who-we-are" | "/work-with-us"

const navigation: Array<{ href: MarketingRoute; label: string }> = [
  { href: "/", label: "Home" },
  { href: "/method", label: "The Acuity Method" },
  { href: "/who-we-are", label: "Who We Are" },
]

function SiteHeader({ current }: { current: MarketingRoute }) {
  return (
    <header className={styles.siteHeader}>
      <Link className={styles.brand} href="/" aria-label="Acuity home">
        <AcuityMark className={styles.brandMark} />
        <span>Acuity</span>
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
        <Link className={styles.workLink} href="/work-with-us#conversation">
          Work With Us
        </Link>
      </div>
    </header>
  )
}

function SiteFooter() {
  return (
    <footer className={styles.siteFooter}>
      <Link className={styles.footerBrand} href="/">
        <AcuityMark className={styles.footerMark} />
        <span>Acuity</span>
      </Link>
      <p>Founder-deployed medical voice.</p>
      <div>
        <Link href="/method">The Acuity Method</Link>
        <Link href="/who-we-are">Who We Are</Link>
        <Link href="/work-with-us#conversation">Work With Us</Link>
        <Link href="/sign-in">Sign in</Link>
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
      <div className={cn(styles.site, sans.variable, display.variable)}>
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
    <div className={styles.venn}>
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
        <strong>The Acuity Method</strong>
      </div>
    </div>
  )
}

function CaseStudySection() {
  return (
    <section className={styles.proofSection}>
      <div>
        <p className={styles.eyebrow}>Six-location ophthalmology group · first 30 days</p>
        <h2>A working operation creates measurable capacity.</h2>
      </div>
      <dl className={styles.proofMetrics}>
        <div><dt>500+</dt><dd>appointments booked into the EMR</dd></div>
        <div><dt>0</dt><dd>missed calls</dd></div>
        <div><dt>500</dt><dd>staff hours returned</dd></div>
        <div><dt>$100K+</dt><dd>estimated revenue recovered</dd></div>
      </dl>
    </section>
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
      <Link className={styles.lightButton} href="/work-with-us#conversation">
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
          <h1>Redesign patient access.</h1>
          <p className={styles.heroBody}>
            Acuity redesigns patient-access workflows, deploys medical voice
            agents across the systems you already use, and stays with your team
            until the new operating model works.
          </p>
          <div className={styles.heroActions}>
            <Link className={styles.darkButton} href="/work-with-us#conversation">
              Work with us <ArrowRight size={17} aria-hidden="true" />
            </Link>
            <Link className={styles.textLink} href="/method">
              See the Acuity Method
            </Link>
          </div>
        </div>
        <PixelWaveField />
      </section>

      <section className={styles.enterpriseDifference}>
        <div className={styles.enterpriseDifferenceLead}>
          <p className={styles.eyebrow}>Built for enterprise</p>
          <h2>Enterprise AI breaks without workflow expertise.</h2>
        </div>
        <div className={styles.enterpriseCommitments}>
          <article>
            <span>Understand</span>
            <h3>Experts in the workflow.</h3>
            <p>Frontline rules and exceptions become the specification.</p>
          </article>
          <article>
            <span>Redesign</span>
            <h3>Fix the operation before automating it.</h3>
            <p>We redesign ownership, handoffs, and escalation paths with your team.</p>
          </article>
          <article>
            <span>Adopt</span>
            <h3>Stay until the change works.</h3>
            <p>The same people deploy, train, review failures, and improve.</p>
          </article>
        </div>
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
            <p className={styles.eyebrow}>The Acuity Method</p>
            <h1>Two capabilities make enterprise AI work.</h1>
          </div>
          <p>
            Acuity combines medical voice system design with the workflow
            expertise needed to deploy it across the enterprise.
          </p>
        </header>
        <MethodVenn />
      </section>

      <CaseStudySection />

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
  name,
  fullName,
  location,
  imageSrc,
  portraitClassName,
  children,
}: {
  name: string
  fullName: string
  location: string
  imageSrc: StaticImageData
  portraitClassName?: string
  children: ReactNode
}) {
  return (
    <article className={styles.founderPanel}>
      <div className={cn(styles.founderPortrait, portraitClassName)}>
        <Image
          alt={`${fullName}, Acuity co-founder`}
          fill
          placeholder="blur"
          sizes="(max-width: 860px) calc(100vw - 48px), 46vw"
          src={imageSrc}
        />
      </div>
      <div className={styles.founderIdentity}>
        <p className={styles.eyebrow}>Co-founder</p>
        <h2>{name}</h2>
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
      <section className={styles.foundingStory}>
        <p className={styles.eyebrow}>The founding story</p>
        <div>
          <h1>Acuity began as a consulting relationship.</h1>
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
              implementation queue. So we built Acuity the same way we learned to
              solve the problem: founder-deployed, inside the operation, with the
              relationship treated as part of the product.
            </p>
          </div>
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

      <section className={styles.foundersSection}>
        <header><p className={styles.eyebrow}>Meet the founders + Engineering team</p><h2>The same people stay in the work.</h2></header>
        <div className={styles.founderGrid}>
          <FounderPanel
            fullName="Kyle Shechtman"
            imageSrc={kylePortrait}
            location="August 2026"
            name="Kyle"
            portraitClassName={styles.founderPortraitKyle}
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
            location="August 2026"
            name="Chase"
            portraitClassName={styles.founderPortraitChase}
          >
            <p>
              When intelligence has negligible marginal cost and is abundant,
              where does value creation accrue? Who owns the interface through
              which intelligence enters the economy?
            </p>
          </FounderPanel>
        </div>
      </section>

      <section className={styles.principlesSection}>
        <p className={styles.eyebrow}>How we work</p>
        <div>
          <article><span>Radical simplicity</span><p>Solve the real problem with fewer moving parts.</p></article>
          <article><span>Craft</span><p>Make the full journey precise and dependable.</p></article>
          <article><span>Failure analysis</span><p>Make failure visible, learn, and improve continuously.</p></article>
        </div>
      </section>

      <PartnershipCta />
    </MarketingFrame>
  )
}

export function WorkWithUsPageContent() {
  return (
    <MarketingFrame current="/work-with-us">
      <section className={styles.workHero}>
        <div>
          <p className={styles.eyebrow}>Work with us</p>
          <h1>Let’s build the patient-access operation together.</h1>
        </div>
        <p>
          Bring us the workflow, the complexity, and the outcome that matters.
          We’ll determine whether it is the right enterprise transformation to
          take on together.
        </p>
      </section>

      <section className={styles.conversationSection} id="conversation">
        <div>
          <p className={styles.eyebrow}>Start the conversation</p>
          <h2>Tell us what needs to change.</h2>
          <p>This is an enterprise operating conversation, not a generic software demo.</p>
          <a
            className={styles.darkButton}
            href="mailto:chase@acuityhealth.io?subject=Work%20with%20Acuity"
          >
            Work with us <ArrowRight size={17} aria-hidden="true" />
          </a>
        </div>
        <div className={styles.conversationPrompts}>
          <p><span>01</span> What breaks in patient access today?</p>
          <p><span>02</span> Which locations and workflows are affected?</p>
          <p><span>03</span> Which systems, rules, and teams are involved?</p>
          <p><span>04</span> What proof would justify expansion?</p>
        </div>
      </section>

      <section className={styles.fitStatement}>
        <p className={styles.eyebrow}>A good fit</p>
        <h2>Multi-site complexity. High-volume work. A committed operator. Real expansion value.</h2>
      </section>
    </MarketingFrame>
  )
}
