import type { ReactNode } from "react"
import type { StaticImageData } from "next/image"
import Image from "next/image"
import Link from "next/link"
import { ArrowRight } from "lucide-react"
import { Geist } from "next/font/google"

import { AcuityMark } from "@/components/acuity-mark"
import {
  PortalSignInTrigger,
  SignInDialog,
} from "@/components/auth/sign-in-dialog"
import { cn } from "@/lib/utils"

import { blogPosts } from "@/lib/blog"

import { BlogCover } from "./blog/blog-cover"
import { CaseStudies } from "./case-studies"
import { PixelWaveField } from "./pixel-wave-field"
import { SiteNavigation, type NavigationFeature } from "./site-navigation"
import { liveKitCaseStudyUrl, modMedListingUrl, stories } from "./stories"
import { StoryArt } from "./story-art"
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
  | "/platform/agents"
  | "/platform/systems"
  | "/platform/deployment"
  | "/platform/strategy"
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
      { href: "/platform/systems", label: "Acuity Systems" },
      { href: "/platform/deployment", label: "Deployment" },
      { href: "/platform/strategy", label: "Strategy" },
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
          <p className={styles.footerDescription}>Applied AI for medical enterprises.</p>
          <Link className={styles.footerContact} href="/work-with-us">
            Build with us
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

function storyFeature(index: number, cta: string): NavigationFeature {
  const story = stories[index]
  return {
    href: story.href,
    external: story.external,
    title: story.title,
    cta,
    art: <StoryArt kind={story.art} />,
  }
}

const latestPost = [...blogPosts].sort((a, b) => b.date.localeCompare(a.date))[0]

const navigationFeatures: Record<string, NavigationFeature> = {
  Platform: storyFeature(1, "Read the LiveKit case study"),
  Integrations: storyFeature(2, "Explore the AdvancedMD integration"),
  Company: {
    href: `/blog/${latestPost.slug}`,
    external: false,
    title: latestPost.title,
    cta: "Read the article",
    art: <BlogCover cover={latestPost.cover} category={latestPost.category} />,
  },
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
        <SiteNavigation current={current} features={navigationFeatures} />
        <main id="main-content">{children}</main>
        <SiteFooter />
      </div>
    </SignInDialog>
  )
}


const stediPartnerUrl = "https://www.stedi.com/platform-partners#acuity-health"

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
            <Link className={styles.textLink} href="/platform/deployment">
              How we deploy
            </Link>
          </div>
        </div>
        <PixelWaveField />
      </section>

      <CaseStudies stories={stories} />
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




function LinkedInLogo() {
  return (
    <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor" aria-hidden="true" focusable="false">
      <path d="M20.45 20.45h-3.56v-5.57c0-1.33-.02-3.04-1.85-3.04-1.85 0-2.14 1.45-2.14 2.94v5.67H9.35V9h3.41v1.56h.05c.48-.9 1.64-1.85 3.37-1.85 3.6 0 4.27 2.37 4.27 5.46v6.28ZM5.34 7.43a2.06 2.06 0 1 1 0-4.13 2.06 2.06 0 0 1 0 4.13ZM7.12 20.45H3.56V9h3.56v11.45ZM22.22 0H1.77C.79 0 0 .77 0 1.73v20.54C0 23.23.79 24 1.77 24h20.45c.98 0 1.78-.77 1.78-1.73V1.73C24 .77 23.2 0 22.22 0Z" />
    </svg>
  )
}

function FounderPanel({
  fullName,
  role,
  linkedinHref,
  imageSrc,
  portraitClassName,
}: {
  fullName: string
  role: string
  linkedinHref: `https://${string}`
  imageSrc: StaticImageData
  portraitClassName?: string
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
          <a href={linkedinHref} rel="noreferrer" target="_blank" aria-label={`${fullName} on LinkedIn`}>
            <LinkedInLogo />
          </a>
        </div>
      </div>
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
            portraitClassName={styles.founderPortraitKyle}
            role="Co-founder & CEO"
          />
          <FounderPanel
            fullName="Chase Fagen"
            imageSrc={chasePortrait}
            linkedinHref="https://www.linkedin.com/in/chase-fagen-198947180"
            portraitClassName={styles.founderPortraitChase}
            role="Co-founder & Head of AI Engineering"
          />
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
