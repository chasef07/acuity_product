import type { ReactNode } from "react"
import type { StaticImageData } from "next/image"
import Image from "next/image"
import Link from "next/link"
import { ArrowRight, Check } from "lucide-react"
import { Geist } from "next/font/google"

import { AcuityMark } from "@/components/acuity-mark"
import {
  PortalSignInTrigger,
  SignInDialog,
} from "@/components/auth/sign-in-dialog"
import { cn } from "@/lib/utils"

import { CaseStudies, type CaseStudy } from "./case-studies"
import { PixelWaveField } from "./pixel-wave-field"
import { SiteNavigation } from "./site-navigation"
import { WorkWithUsForm } from "./work-with-us-form"
import styles from "./enterprise-site.module.css"

import chasePortrait from "../../../public/marketing/chase-fagen-v2.png"
import kylePortrait from "../../../public/marketing/kyle-shechtman-2026.png"
import venincasaPortrait from "../../../public/marketing/michael-venincasa-md.jpg"

const display = Geist({
  subsets: ["latin"],
  variable: "--font-acuity-display",
})

export type MarketingRoute =
  | "/"
  | "/method"
  | "/platform/agents"
  | "/platform/systems"
  | "/integrations"
  | "/integrations/advancedmd"
  | "/integrations/stedi"
  | "/who-we-are"
  | "/work-with-us"
  | "/privacy-policy"
  | "/terms-of-service"
  | "/security"
  | "/blog"

const footerNavigation = [
  {
    label: "Platform",
    links: [
      { href: "/platform/agents", label: "Acuity Agents" },
      { href: "/method", label: "Deployment Strategy" },
      { href: "/platform/systems", label: "Acuity Systems" },
      { href: "/integrations", label: "Partnerships & Integrations" },
    ],
  },
  {
    label: "Company",
    links: [
      { href: "/who-we-are", label: "Who We Are" },
      { href: "/blog", label: "Blog" },
      { href: "/work-with-us", label: "Work With Us" },
      { href: "/sign-in", label: "Sign in" },
    ],
  },
  {
    label: "Social",
    links: [
      {
        href: "https://www.linkedin.com/company/acuityhealth/",
        label: "LinkedIn",
        external: true,
      },
    ],
  },
  {
    label: "Legal",
    links: [
      { href: "/security", label: "Security, Privacy & HIPAA" },
      { href: "/privacy-policy", label: "Privacy Policy" },
      { href: "/terms-of-service", label: "Terms of Service" },
    ],
  },
] as const

function SiteFooter() {
  return (
    <footer className={styles.siteFooter}>
      <div className={styles.footerPrimary}>
        <div className={styles.footerIdentity}>
          <Link className={styles.footerBrand} href="/" aria-label="Acuity Health home">
            <AcuityMark className={styles.footerBrandMark} />
            <span>Acuity Health</span>
          </Link>
          <p className={styles.footerDescription}>Voice AI agents for patient access.</p>
          <Link className={styles.footerContact} href="/work-with-us">
            Work with us
          </Link>
          <p className={styles.footerCopyright}>© {new Date().getFullYear()} Acuity Health.</p>
        </div>
        <nav className={styles.footerNavigation} aria-label="Footer navigation">
          {footerNavigation.map((section) => (
            <section className={styles.footerGroup} key={section.label}>
              <h2>{section.label}</h2>
              <ul>
                {section.links.map((link) => (
                  <li key={link.href}>
                    {link.href === "/sign-in" ? (
                      <PortalSignInTrigger />
                    ) : "external" in link ? (
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
      <div className={cn(styles.site, display.variable)}>
        <a className={styles.skipLink} href="#main-content">
          Skip to main content
        </a>
        <SiteNavigation current={current} />
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

function MethodCapabilities({ headingLevel = 3 }: { headingLevel?: 2 | 3 }) {
  const Heading = headingLevel === 2 ? "h2" : "h3"

  return (
    <div className={styles.methodCapabilities} data-testid="method-capabilities">
      <div className={styles.methodColumns}>
        <article className={styles.methodSystem}>
          <p className={styles.methodLabel}>The technology</p>
          <Heading>Agentic<br />system design</Heading>
          <p className={styles.methodDescription}>
            Voice AI agents built around medical workflows, privacy, and control.
          </p>
          <ul>
            <li>Data security + HIPAA safeguards</li>
            <li>Analytics + guardrails</li>
            <li>EMR integrations</li>
          </ul>
        </article>
        <article className={styles.methodWorkflow}>
          <p className={styles.methodLabel}>The operation</p>
          <Heading>Workflow<br />transformation</Heading>
          <p className={styles.methodDescription}>
            Workflows shaped with the people who run them, from rollout onward.
          </p>
          <ul>
            <li>Change management</li>
            <li>Workflow redesign</li>
            <li>Frontline training</li>
          </ul>
        </article>
      </div>
      <div className={styles.methodConnection}>
        <p>Built together.<br />Improved in the operation.</p>
        <span>One team, from system design to the way work gets done.</span>
      </div>
    </div>
  )
}

const liveKitCaseStudyUrl = "https://livekit.com/blog/how-acuity-health-transforms-patient-access-with-livekit-voice-agents"
const stediPartnerUrl = "https://www.stedi.com/platform-partners#acuity-health"
const modMedListingUrl = "https://modmed.my.site.com/synapsysmarketplace/s/partner-app/a9YUW000000jfGj2AI/data-buddies-solutions-llc"

const caseStudies: CaseStudy[] = [
  {
    tab: "LiveKit",
    eyebrow: "Case study · LiveKit",
    title: "AI agents that handle the whole call.",
    body: "Acuity’s agents run on LiveKit. Across every use case they fully contain 80% of calls, and most bookings finish in under a minute.",
    metrics: [
      { value: "80%", label: "Calls fully contained by AI agents" },
      { value: "<1 min", label: "Most bookings completed" },
      { value: "81%", label: "Booking calls that end with a “thank you”" },
    ],
    link: { href: liveKitCaseStudyUrl, label: "Read the LiveKit case study", external: true },
  },
  {
    tab: "AdvancedMD",
    eyebrow: "Marketplace partner · AdvancedMD",
    title: "Patient calls become appointments in AdvancedMD.",
    body: "Our agents book with your practice’s providers, locations, appointment types, and scheduling rules.",
    link: { href: "/integrations/advancedmd", label: "Explore the AdvancedMD integration" },
  },
  {
    tab: "ModMed",
    eyebrow: "Marketplace listing · ModMed",
    title: "Listed on the ModMed SynapSYS Marketplace.",
    body: "Practices using ModMed can find Acuity Health there. We scope the supported workflows with your team before rollout.",
    link: { href: modMedListingUrl, label: "View our ModMed listing", external: true },
  },
  {
    tab: "Whitecotton Vision",
    eyebrow: "Sales & implementation partner · Whitecotton Vision",
    title: "Brought to eye care by Whitecotton Vision.",
    body: "Whitecotton Vision brings Acuity Health to ophthalmology and optometry practices and supports implementation.",
    link: { href: "https://whitecottonvision.com/acuity-health", label: "Explore Acuity with Whitecotton Vision", external: true },
  },
]

export function EnterpriseHome({
  initiallyOpen = false,
}: {
  initiallyOpen?: boolean
}) {
  return (
    <MarketingFrame current="/" initiallyOpen={initiallyOpen}>
      <section className={styles.hero}>
        <div className={styles.heroCopy}>
          <h1>Applied AI<br />for medical enterprises.</h1>
          <p className={styles.heroBody}>
            Acuity is the infrastructure platform and partner for medical practices
            to deploy, measure, and continuously improve AI agents they own,
            starting with the phone.
          </p>
          <div className={styles.heroActions}>
            <Link className={styles.darkButton} href="/work-with-us">
              Build with us
            </Link>
            <Link className={styles.textLink} href="/method">
              Explore our method
            </Link>
          </div>
        </div>
        <PixelWaveField />
      </section>

      <CaseStudies stories={caseStudies} />

      <section className={styles.methodSection}>
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
        <MethodCapabilities />
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
            Acuity grew out of our consulting work with medical practices. We build
            voice AI agents around real workflows and support teams through rollout.
          </p>
          <Link className={styles.inlineArrowLink} href="/who-we-are">
            Read our story <ArrowRight size={16} aria-hidden="true" />
          </Link>
        </div>
      </section>
    </MarketingFrame>
  )
}


export function IntegrationsPageContent() {
  return (
    <MarketingFrame current="/integrations">
      <section className={styles.missionVision} aria-labelledby="integrations-title" id="integrations">
        <div className={styles.missionStatement}>
          <p className={styles.eyebrow}>Partnerships & integrations</p>
          <h1 id="integrations-title">Integrations and partners built around the systems your practice already uses.</h1>
        </div>
        <div className={styles.visionStatement}>
          <p className={styles.eyebrow}>From the call to the next step</p>
          <p>Connect patient conversations to scheduling and insurance eligibility, shaped around the way your team works.</p>
        </div>
      </section>

      <section className={styles.integrationEditorial} aria-labelledby="livekit-partner">
        <header>
          <p className={styles.eyebrow}>Voice AI infrastructure · Featured case study</p>
          <h2 id="livekit-partner">LiveKit</h2>
        </header>
        <div className={styles.storyBody}>
          <p>Our voice agents run on LiveKit. Building agents as code lets us tailor each one to a practice, and LiveKit Simulations tests every release against patient profiles and edge cases before it reaches patients.</p>
          <div className={styles.platformActions}>
            <a className={styles.inlineArrowLink} href={liveKitCaseStudyUrl} target="_blank" rel="noopener">
              Read how Acuity Health transforms patient access with LiveKit <ArrowRight size={16} aria-hidden="true" />
            </a>
          </div>
        </div>
      </section>

      <section className={styles.integrationEditorial} aria-labelledby="advancedmd-partner">
        <header>
          <p className={styles.eyebrow}>EHR & PMS · Marketplace partner</p>
          <h2 id="advancedmd-partner">AdvancedMD</h2>
        </header>
        <div className={styles.storyBody}>
          <p>Patient calls become appointments in AdvancedMD. Our AI receptionist works with your practice’s providers, locations, appointment types, and scheduling rules.</p>
          <div className={styles.platformActions}>
            <Link className={styles.inlineArrowLink} href="/integrations/advancedmd">
              Explore the AdvancedMD integration <ArrowRight size={16} aria-hidden="true" />
            </Link>
            <a className={styles.inlineArrowLink} href="https://www.advancedmd.com/integrations/marketplace/acuity-health/" target="_blank" rel="noopener">
              View our AdvancedMD marketplace listing <ArrowRight size={16} aria-hidden="true" />
            </a>
          </div>
        </div>
      </section>

      <section className={styles.integrationEditorial} aria-labelledby="modmed-marketplace">
        <header>
          <p className={styles.eyebrow}>EHR & PMS · SynapSYS Marketplace</p>
          <h2 id="modmed-marketplace">ModMed</h2>
        </header>
        <div className={styles.storyBody}>
          <p>Acuity Health is available on the ModMed SynapSYS Marketplace. Practices using ModMed can find our AI receptionist there, and we scope the supported workflows with your team before rollout.</p>
          <div className={styles.platformActions}>
            <a className={styles.inlineArrowLink} href={modMedListingUrl} target="_blank" rel="noopener">
              View our ModMed SynapSYS Marketplace listing <ArrowRight size={16} aria-hidden="true" />
            </a>
          </div>
        </div>
      </section>

      <section className={styles.integrationEditorial} aria-labelledby="stedi-partner">
        <header>
          <p className={styles.eyebrow}>Healthcare clearinghouse · Platform partner</p>
          <h2 id="stedi-partner">Stedi</h2>
        </header>
        <div className={styles.storyBody}>
          <p>Bring insurance eligibility into the patient-access workflow. We partner with Stedi, a healthcare clearinghouse, to support quick, efficient medical, vision, and Medicare eligibility checks for supported payers.</p>
          <div className={styles.platformActions}>
            <Link className={styles.inlineArrowLink} href="/integrations/stedi">
              Explore insurance eligibility <ArrowRight size={16} aria-hidden="true" />
            </Link>
            <a className={styles.inlineArrowLink} href={stediPartnerUrl} target="_blank" rel="noopener">
              View Acuity on Stedi <ArrowRight size={16} aria-hidden="true" />
            </a>
          </div>
        </div>
      </section>

      <section className={styles.integrationEditorial} aria-labelledby="whitecotton-partner">
        <header>
          <p className={styles.eyebrow}>Eye care · Sales & implementation partner</p>
          <h2 id="whitecotton-partner">Whitecotton Vision</h2>
        </header>
        <div className={styles.storyBody}>
          <p>Whitecotton Vision brings Acuity Health to ophthalmology and optometry practices alongside its ophthalmic equipment and practice technology. Their team helps practices assess fit and supports implementation.</p>
          <div className={styles.platformActions}>
            <a className={styles.inlineArrowLink} href="https://whitecottonvision.com/acuity-health" target="_blank" rel="noopener">
              Explore Acuity with Whitecotton Vision <ArrowRight size={16} aria-hidden="true" />
            </a>
          </div>
        </div>
      </section>

      <section className={styles.integrationEditorial} aria-labelledby="other-systems">
        <header>
          <p className={styles.eyebrow}>Your systems. Your workflow.</p>
          <h2 id="other-systems">Other EHR & PMS systems</h2>
        </header>
        <div className={styles.storyBody}>
          <p>We scope new EHR and PM connections with each practice, including Nextech, athenahealth and Compulink.</p>
          <p>Use another system? We can assess a custom connection around the work your practice needs.</p>
        </div>
      </section>

      <section className={styles.integrationEditorial} aria-labelledby="integration-setup">
        <header>
          <p className={styles.eyebrow}>How we build</p>
          <h2 id="integration-setup">How integration setup works</h2>
        </header>
        <div className={styles.storyBody}>
          <p>We map the work with your team: scheduling rules, insurance requirements, and the moments that need staff attention.</p>
          <p>Then we confirm system access and payer enrollment, configure the workflow, and verify the connection before rollout. Your team stays involved as we refine the operation together.</p>
        </div>
      </section>
    </MarketingFrame>
  )
}

export function StediPageContent() {
  return (
    <MarketingFrame current="/integrations/stedi">
      <section className={styles.missionVision} aria-labelledby="stedi-title">
        <div className={styles.missionStatement}>
          <p className={styles.eyebrow}>Stedi · Healthcare clearinghouse partner</p>
          <h1 id="stedi-title">Medical, vision, and Medicare eligibility checks.</h1>
        </div>
        <div className={styles.visionStatement}>
          <p className={styles.eyebrow}>Insurance eligibility, connected</p>
          <p>We partner with Stedi to bring quick, efficient eligibility checks into your patient-access workflow.</p>
        </div>
      </section>

      <section className={styles.integrationEditorial} aria-labelledby="stedi-connection">
        <header>
          <p className={styles.eyebrow}>Our partnership</p>
          <h2 id="stedi-connection">A clearinghouse connection. A clearer next step.</h2>
        </header>
        <div className={styles.storyBody}>
          <p>Stedi connects eligibility requests to insurance payers. Acuity brings that connection into the work of answering patient calls and preparing the next step with your practice.</p>
          <p>Real-time checks help reduce manual verification. Available benefits and response times depend on the payer, the patient information provided, and your practice’s enrollment.</p>
          <div className={styles.platformActions}>
            <a className={styles.inlineArrowLink} href="https://www.stedi.com/blog/spotlight-chase-fagen-acuity-health" target="_blank" rel="noopener">
              Read our CTO’s thoughts on Stedi <ArrowRight size={16} aria-hidden="true" />
            </a>
            <a className={styles.inlineArrowLink} href={stediPartnerUrl} target="_blank" rel="noopener">
              View Acuity on Stedi <ArrowRight size={16} aria-hidden="true" />
            </a>
          </div>
        </div>
      </section>

      <section className={styles.integrationsSection} aria-labelledby="eligibility-types">
        <header className={styles.sectionHeader}>
          <div><p className={styles.eyebrow}>Coverage checks</p><h2 id="eligibility-types">The right check for the visit.</h2></div>
          <p>We confirm payer support and the information your team needs before configuring your eligibility workflow.</p>
        </header>
        <div className={styles.platformDetails}>
          <article>
            <h2>Medical eligibility</h2>
            <p>Check active medical coverage and the benefits returned by supported payers, including copays and deductibles when available.</p>
          </article>
          <article>
            <h2>Vision eligibility</h2>
            <p>Check vision benefits where the payer supports them. We scope routine vision and medical eye-care workflows separately so the check fits the visit.</p>
          </article>
          <article>
            <h2>Medicare eligibility</h2>
            <p>Support Medicare eligibility checks with the required provider enrollment. Medicare Advantage checks depend on the patient’s plan and the payer connection.</p>
          </article>
        </div>
      </section>

      <section className={styles.integrationEditorial} aria-labelledby="eligibility-questions">
        <header>
          <p className={styles.eyebrow}>Before rollout</p>
          <h2 id="eligibility-questions">Know what the check can tell you.</h2>
        </header>
        <div className={styles.eligibilityAnswers}>
          <div><h3>Does this support every payer?</h3><p>Support varies by payer and coverage type. We review your payer mix and any enrollment requirements with your team. You can also explore <a href="https://www.stedi.com/healthcare/network" target="_blank" rel="noopener">Stedi’s payer network</a>.</p></div>
          <div><h3>Does eligibility guarantee payment?</h3><p>No. An eligibility response is not a guarantee of payment, prior authorization, or confirmation that a particular procedure is covered. Your practice’s acceptance rules and any remaining verification still apply.</p></div>
          <div><h3>What if a check cannot be completed?</h3><p>We define the staff handoff during setup, including when patient information needs correction or a payer cannot return a usable result.</p></div>
          <Link className={styles.inlineArrowLink} href="/integrations">All partnerships & integrations <ArrowRight size={16} aria-hidden="true" /></Link>
        </div>
      </section>
    </MarketingFrame>
  )
}

export function AdvancedMDPageContent() {
  return (
    <MarketingFrame current="/integrations/advancedmd">
      <section className={styles.integrationsSection}>
        <header className={styles.sectionHeader}>
          <div>
            <p className={styles.eyebrow}>AdvancedMD marketplace partner</p>
            <h1>AI Agents for AdvancedMD.</h1>
          </div>
          <p>
            Acuity Health answers patient calls and schedules directly in
            AdvancedMD, with medical AI built around your practice’s rules.
          </p>
        </header>
        <div className={styles.platformActions}>
          <Link className={styles.darkButton} href="/work-with-us">
            Discuss AdvancedMD <ArrowRight size={16} aria-hidden="true" />
          </Link>
          <a
            className={styles.inlineArrowLink}
            href="https://www.advancedmd.com/integrations/marketplace/acuity-health/"
            rel="noopener"
            target="_blank"
          >
            View our AdvancedMD marketplace listing
            <ArrowRight size={16} aria-hidden="true" />
          </a>
        </div>
        <div className={styles.platformDetails}>
          <article>
            <h2>Calls answered, 24/7</h2>
            <p>Handle common questions, scheduling requests, and after-hours calls.</p>
          </article>
          <article>
            <h2>Scheduling that follows your rules</h2>
            <p>Book in AdvancedMD using your providers, locations, appointment types, and insurance requirements.</p>
          </article>
          <article>
            <h2>Clear staff handoffs</h2>
            <p>Turn unresolved requests into staff work with the context, ownership, and next action to follow through.</p>
          </article>
        </div>
        <div className={styles.customIntegration}>
          <div>
            <h2>What does setup involve?</h2>
            <p className={styles.integrationDescription}>
              We review your AdvancedMD setup and scheduling workflows with your
              team, then confirm the integration requirements and work your AI
              should handle before rollout.
            </p>
          </div>
          <Link className={styles.inlineArrowLink} href="/integrations">
            All EHR & PMS integrations <ArrowRight size={16} aria-hidden="true" />
          </Link>
        </div>
      </section>
    </MarketingFrame>
  )
}

export function MethodPageContent() {
  return (
    <MarketingFrame current="/method">
      <section className={styles.methodSection}>
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
        <MethodCapabilities headingLevel={2} />
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
    </MarketingFrame>
  )
}

export function AgentsPageContent() {
  return (
    <MarketingFrame current="/platform/agents">
      <section className={styles.missionVision} aria-labelledby="agents-title">
        <div className={styles.missionStatement}>
          <p className={styles.eyebrow}>Acuity Agents</p>
          <h1 id="agents-title">AI agents for the patient phone line.</h1>
        </div>
        <div className={styles.visionStatement}>
          <p className={styles.eyebrow}>What the agent does</p>
          <p>Answer patient calls, book in your practice management system, confirm accepted insurance, and hand anything unresolved to staff with context.</p>
        </div>
      </section>

      <section className={styles.integrationsSection} aria-labelledby="agent-work">
        <header className={styles.sectionHeader}>
          <div>
            <p className={styles.eyebrow}>Built around your rules</p>
            <h2 id="agent-work">The work an agent takes off the front desk.</h2>
          </div>
          <p>Each agent is configured for one practice: its providers, locations, appointment types, payers, and the moments that need a person.</p>
        </header>
        <div className={cn(styles.platformDetails, styles.platformDetailsFour)}>
          <article>
            <h2>Answer the call</h2>
            <p>Handle common questions, scheduling requests, and after-hours calls from your practice’s own knowledge base.</p>
          </article>
          <article>
            <h2>Book in your system</h2>
            <p>Schedule directly in AdvancedMD with your providers, locations, appointment types, and scheduling rules.</p>
          </article>
          <article>
            <h2>Confirm accepted insurance</h2>
            <p>Tell callers whether your practice accepts their plan, from your own insurance list.</p>
          </article>
          <article>
            <h2>Hand off with context</h2>
            <p>Turn unresolved requests into staff tasks with the call and what the patient needs.</p>
          </article>
        </div>
      </section>

      <section className={styles.integrationEditorial} aria-labelledby="agent-testing">
        <header>
          <p className={styles.eyebrow}>Built as code</p>
          <h2 id="agent-testing">Tailored to each practice. Tested against real scenarios.</h2>
        </header>
        <div className={styles.storyBody}>
          <p>Our agents run on LiveKit. Building them as code lets us tailor each one to a practice, and we pressure-test them with LiveKit Simulations against patient profiles and edge cases.</p>
          <p>Across every use case, Acuity’s agents fully contain 80% of calls, and 81% of booking calls end with a “thank you.”</p>
          <div className={styles.platformActions}>
            <a className={styles.inlineArrowLink} href={liveKitCaseStudyUrl} target="_blank" rel="noopener">
              Read the LiveKit case study <ArrowRight size={16} aria-hidden="true" />
            </a>
            <Link className={styles.inlineArrowLink} href="/platform/systems">
              See the staff workspace <ArrowRight size={16} aria-hidden="true" />
            </Link>
          </div>
        </div>
      </section>
    </MarketingFrame>
  )
}

export function SystemsPageContent() {
  return (
    <MarketingFrame current="/platform/systems">
      <section className={styles.missionVision} aria-labelledby="systems-title">
        <div className={styles.missionStatement}>
          <p className={styles.eyebrow}>Acuity Systems</p>
          <h1 id="systems-title">The staff workspace behind every agent.</h1>
        </div>
        <div className={styles.visionStatement}>
          <p className={styles.eyebrow}>One place for the work</p>
          <p>The requests agents hand off, the calls they handle, and the numbers that show whether it is working.</p>
        </div>
      </section>

      <section className={styles.integrationsSection} aria-labelledby="systems-work">
        <header className={styles.sectionHeader}>
          <div>
            <p className={styles.eyebrow}>For practice staff</p>
            <h2 id="systems-work">Agents do the routine. Staff see everything.</h2>
          </div>
          <p>Your team works handoffs, reviews calls, and keeps the agent’s answers current from one workspace.</p>
        </header>
        <div className={styles.platformDetails}>
          <article>
            <h2>Task inbox</h2>
            <p>Requests an agent could not finish arrive as tasks, with the call and what the patient needs.</p>
          </article>
          <article>
            <h2>Calls and texts</h2>
            <p>Call and text patients back from the same workspace, without switching tools.</p>
          </article>
          <article>
            <h2>Call history</h2>
            <p>Review every call your agent handled, with the full transcript.</p>
          </article>
          <article>
            <h2>Analytics</h2>
            <p>Track bookings, call volume, call duration, and staff activity, day by day.</p>
          </article>
          <article>
            <h2>Knowledge base</h2>
            <p>Keep the answers your agent gives current, with a history of every change.</p>
          </article>
        </div>
        <div className={styles.customIntegration}>
          <div>
            <h2>How do we roll it out?</h2>
            <p className={styles.integrationDescription}>
              We prove the workflow in your operation with your team, then expand.
            </p>
          </div>
          <Link className={styles.inlineArrowLink} href="/method">
            Our deployment strategy <ArrowRight size={16} aria-hidden="true" />
          </Link>
        </div>
      </section>
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
                jobTitle: "Co-founder and Head of AI Engineering",
                sameAs: ["https://www.linkedin.com/in/chase-fagen-198947180"],
                worksFor: { "@id": "https://acuityhealth.io/#organization" },
              },
              {
                "@type": "Person",
                "@id": "https://acuityhealth.io/who-we-are#dr-venincasa",
                name: "Michael Venincasa, MD",
                jobTitle: "Chief Medical Officer",
                image: {
                  "@type": "ImageObject",
                  url: "https://acuityhealth.io/marketing/michael-venincasa-md.jpg",
                  width: venincasaPortrait.width,
                  height: venincasaPortrait.height,
                  caption: "Michael Venincasa, MD, Acuity Health Chief Medical Officer",
                },
                worksFor: { "@id": "https://acuityhealth.io/#organization" },
              },
            ],
          }).replace(/</g, "\\u003c"),
        }}
        type="application/ld+json"
      />
      <section
        aria-labelledby="company-mission"
        className={styles.missionVision}
      >
        <div className={styles.missionStatement}>
          <p className={styles.eyebrow}>Our mission</p>
          <h1 id="company-mission">
            Free medical practices from administrative overload so every patient
            can be treated like a VIP.
          </h1>
        </div>
        <div className={styles.visionStatement}>
          <p className={styles.eyebrow}>Our vision</p>
          <p>
            A future where AI runs the administration, humans elevate the care,
            and no patient falls through the cracks.
          </p>
        </div>
      </section>

      <section className={styles.foundingStory} aria-labelledby="founding-story">
        <header>
          <p className={styles.eyebrow}>The founding story</p>
          <h2 id="founding-story">From consulting<br />to Acuity.</h2>
        </header>
        <div className={styles.storyBody}>
          <p>
            Acuity grew out of consulting work with medical practices. We learned
            how scheduling rules, insurance requirements, and handoffs shape the
            work of staff and the experience of patients.
          </p>
          <p>
            We built our voice AI agents around those realities, connecting them
            to existing systems and giving staff clear roles in the workflow.
          </p>
          <p>
            Today, our founders stay involved from workflow design through rollout.
            We build alongside your team and keep improving the operation together.
          </p>
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
            role="Co-founder & Head of AI Engineering"
          >
            <p>
              When intelligence has negligible marginal cost and is abundant,
              where does value creation accrue? Who owns the interface through
              which intelligence enters the economy?
            </p>
          </FounderPanel>
        </div>
        <article
          aria-labelledby="dr-venincasa"
          className={styles.clinicalLeadership}
        >
          <Image
            alt="Michael Venincasa, MD, Acuity Health Chief Medical Officer"
            className={styles.clinicalPortrait}
            placeholder="blur"
            loading="lazy"
            sizes="(max-width: 427px) calc(100vw - 48px), (max-width: 640px) 380px, (max-width: 860px) 300px, 380px"
            src={venincasaPortrait}
          />
          <div className={styles.clinicalProfile}>
            <p className={styles.eyebrow}>Clinical leadership</p>
            <h2 id="dr-venincasa">Michael Venincasa<span>, MD</span></h2>
            <p className={styles.clinicalRole}>Chief Medical Officer</p>
            <div className={styles.clinicalBio}>
              <p>
                Dr. Venincasa is a comprehensive ophthalmologist with a special
                interest in cataract surgery and advanced lens replacement.
              </p>
              <p>
                He earned his medical degree at the University of Miami Miller
                School of Medicine and completed his residency at Bascom Palmer
                Eye Institute. His commitment to improving patients’ vision and
                lives guides his work at Acuity Health, where he helps shape AI
                that gives medical teams more time to focus on care.
              </p>
            </div>
          </div>
        </article>
      </section>

      <section className={styles.principlesSection}>
        <header>
          <p className={styles.eyebrow}>Our principles</p>
          <h2>How we build</h2>
        </header>
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
            <h2>Baseline</h2>
            <p>Agree on volumes, failure modes, handoffs, and the KPIs that matter.</p>
          </article>
          <article>
            <span>02</span>
            <h2>Redesign</h2>
            <p>Map the rules, exceptions, ownership, and new workflow with the people who run it.</p>
          </article>
          <article>
            <span>03</span>
            <h2>Test</h2>
            <p>Launch a bounded pilot that tests the highest-risk assumptions in real operations.</p>
          </article>
          <article>
            <span>04</span>
            <h2>Prove</h2>
            <p>Compare results with the agreed KPIs, strengthen weak points, and expand when the evidence holds.</p>
          </article>
        </div>
      </section>
    </MarketingFrame>
  )
}
