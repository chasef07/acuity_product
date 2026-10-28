import type { ReactNode } from "react"
import Link from "next/link"
import {
  BarChart3,
  BookOpen,
  Bot,
  CalendarCheck,
  Check,
  Code2,
  FileText,
  Inbox,
  MapPin,
  MoonStar,
  ShieldCheck,
  Users,
} from "lucide-react"

import { Atmosphere, type AtmosphereShape, type AtmosphereTone } from "./atmosphere"
import { CaseStudies } from "./case-studies"
import { MarketingFrame, type MarketingRoute } from "./enterprise-site"
import { FeatureAccordion, PhaseTabs } from "./platform-interactive"
import { stories, type StoryArt as StoryArtKind } from "./stories"
import styles from "./platform.module.css"

type Tone = AtmosphereTone
type Shape = AtmosphereShape

function PageHero({
  tone,
  art,
  eyebrow,
  title,
  lead,
}: {
  tone: Tone
  art: StoryArtKind
  eyebrow: string
  title: string
  lead: string
}) {
  return (
    <Atmosphere tone={tone} shape="a" art={art} className={styles.hero}>
      <p className={styles.heroEyebrow}>{eyebrow}</p>
      <h1>{title}</h1>
      <p>{lead}</p>
    </Atmosphere>
  )
}

function Tiles({
  items,
}: {
  items: Array<{ title: string; body: string; tone: Tone; shape: Shape; art: StoryArtKind }>
}) {
  return (
    <div className={styles.tiles}>
      {items.map((item) => (
        <Atmosphere tone={item.tone} shape={item.shape} art={item.art} className={styles.tile} key={item.title}>
          <h3>{item.title}</h3>
          <p>{item.body}</p>
        </Atmosphere>
      ))}
    </div>
  )
}

function FeatureGrid({
  items,
}: {
  items: Array<{ icon: ReactNode; title: string; body: string }>
}) {
  return (
    <ul className={styles.featureGrid}>
      {items.map((item) => (
        <li key={item.title}>
          {item.icon}
          <h3>{item.title}</h3>
          <p>{item.body}</p>
        </li>
      ))}
    </ul>
  )
}

const products: Array<{
  href: MarketingRoute
  name: string
  body: string
  tone: Tone
  shape: Shape
  art: StoryArtKind
}> = [
  {
    href: "/platform/agents",
    name: "Acuity Agents",
    body: "Voice agents that answer, book, and hand off.",
    tone: "mist",
    shape: "b",
    art: "wave",
  },
  {
    href: "/platform/systems",
    name: "Acuity Systems",
    body: "The staff workspace behind every agent.",
    tone: "stone",
    shape: "c",
    art: "network",
  },
  {
    href: "/platform/deployment",
    name: "Deployment",
    body: "On-site with your team until you own it.",
    tone: "dusk",
    shape: "a",
    art: "rings",
  },
  {
    href: "/platform/strategy",
    name: "Strategy",
    body: "Start with one workflow. Scale on evidence.",
    tone: "ember",
    shape: "b",
    art: "calendar",
  },
]

function PlatformTrio({ current }: { current: MarketingRoute }) {
  return (
    <section className={styles.section} aria-labelledby="platform-title">
      <h2 className={styles.sectionTitle} id="platform-title">
        One platform for patient access
      </h2>
      <div className={styles.trio}>
        {products
          .filter((product) => product.href !== current)
          .map((product) => (
            <Link href={product.href} key={product.href}>
              <Atmosphere tone={product.tone} shape={product.shape} art={product.art} className={styles.trioCard}>
                <strong>{product.name}</strong>
                <div>
                  <p>{product.body}</p>
                  <span>Explore</span>
                </div>
              </Atmosphere>
            </Link>
          ))}
      </div>
    </section>
  )
}

function CtaBand() {
  return (
    <Atmosphere tone="dusk" shape="c" art="wave" className={styles.ctaBand}>
      <h2>Let’s redesign one patient-access workflow together.</h2>
      <Link className={styles.pill} href="/work-with-us">
        Build with us
      </Link>
    </Atmosphere>
  )
}

function Visual({ tone, art }: { tone: Tone; art: StoryArtKind }) {
  return (
    <Atmosphere tone={tone} shape="b" art={art} className={styles.accordionVisual} />
  )
}

const iconProps = { size: 22, strokeWidth: 1.5, "aria-hidden": true } as const

export function AgentsPageContent() {
  return (
    <MarketingFrame current="/platform/agents">
      <PageHero
        tone="mist"
        art="wave"
        eyebrow="Acuity Agents"
        title="AI agents for the patient phone line."
        lead="Acuity Agents answer patient calls, book in your practice management system, and hand everything else to your staff with context."
      />

      <section className={styles.section} aria-labelledby="agent-work">
        <h2 className={styles.sectionTitle} id="agent-work">
          The work an agent takes off the front desk
        </h2>
        <Tiles
          items={[
            {
              title: "Answer the call",
              body: "Common questions, scheduling requests, and after-hours calls, answered from your practice’s own knowledge base.",
              tone: "dusk",
              shape: "a",
              art: "wave",
            },
            {
              title: "Book in your system",
              body: "Appointments booked directly in AdvancedMD with your providers, locations, appointment types, and rules.",
              tone: "mist",
              shape: "b",
              art: "calendar",
            },
            {
              title: "Hand off with context",
              body: "Anything the agent can’t finish becomes a staff task with the call and what the patient needs.",
              tone: "stone",
              shape: "c",
              art: "network",
            },
          ]}
        />
      </section>

      <section className={`${styles.section} ${styles.sectionCanvas}`} aria-labelledby="agent-fit">
        <h2 className={styles.sectionTitle} id="agent-fit">
          Built around how your practice works
        </h2>
        <FeatureAccordion
          items={[
            {
              title: "Your scheduling rules",
              body: "Each agent books with your providers, locations, appointment types, and the rules that decide who sees whom.",
              visual: <Visual tone="mist" art="calendar" />,
            },
            {
              title: "Your insurance list",
              body: "Callers hear whether your practice accepts their plan, from the insurance list your staff maintains.",
              visual: <Visual tone="sand" art="rings" />,
            },
            {
              title: "Your knowledge base",
              body: "Answers come from your practice’s own knowledge base, which your team keeps current with a history of every change.",
              visual: <Visual tone="stone" art="network" />,
            },
            {
              title: "Tested against real scenarios",
              body: "Agents are built as code and pressure-tested with LiveKit Simulations against patient profiles and edge cases.",
              visual: <Visual tone="dusk" art="wave" />,
            },
          ]}
        />
      </section>

      <CaseStudies stories={stories} />

      <section className={`${styles.section} ${styles.sectionCanvas}`} aria-labelledby="agent-features">
        <h2 className={styles.sectionTitle} id="agent-features">
          Built to run inside a medical practice
        </h2>
        <FeatureGrid
          items={[
            { icon: <MoonStar {...iconProps} />, title: "After-hours coverage", body: "Calls that arrive after the office closes still get answered." },
            { icon: <CalendarCheck {...iconProps} />, title: "Direct booking", body: "Appointments land in AdvancedMD, not in a message queue." },
            { icon: <FileText {...iconProps} />, title: "Every call transcribed", body: "Staff can review each call the agent handled, with the full transcript." },
            { icon: <BarChart3 {...iconProps} />, title: "Measured", body: "Bookings, call volume, and call duration, tracked day by day." },
            { icon: <ShieldCheck {...iconProps} />, title: "HIPAA safeguards", body: "Built with the privacy and security controls described on our Security page." },
            { icon: <Code2 {...iconProps} />, title: "Built as code", body: "Each agent is tailored to one practice, not configured from a template." },
          ]}
        />
      </section>

      <PlatformTrio current="/platform/agents" />
      <CtaBand />
    </MarketingFrame>
  )
}

export function SystemsPageContent() {
  return (
    <MarketingFrame current="/platform/systems">
      <PageHero
        tone="stone"
        art="network"
        eyebrow="Acuity Systems"
        title="The staff workspace behind every agent."
        lead="The requests agents hand off, the calls they handle, and the numbers that show whether it is working, in one place."
      />

      <section className={styles.section} aria-labelledby="systems-why">
        <div className={styles.twoColumn}>
          <h2 className={styles.sectionTitle} id="systems-why">
            Agents do the routine. Staff see everything.
          </h2>
          <ul className={styles.checkList}>
            <li><Check size={18} aria-hidden="true" /> Work arrives as tasks, not voicemails to transcribe.</li>
            <li><Check size={18} aria-hidden="true" /> Staff call and text patients back without switching tools.</li>
            <li><Check size={18} aria-hidden="true" /> Every call the agent handled is there to review.</li>
          </ul>
        </div>
        <Tiles
          items={[
            {
              title: "Task inbox",
              body: "Requests an agent could not finish arrive as tasks, with the call and what the patient needs.",
              tone: "sand",
              shape: "a",
              art: "network",
            },
            {
              title: "Calls and texts",
              body: "Call and text patients back from the same workspace.",
              tone: "dusk",
              shape: "b",
              art: "wave",
            },
            {
              title: "Call history",
              body: "Review every call your agent handled, with the full transcript.",
              tone: "mist",
              shape: "c",
              art: "rings",
            },
          ]}
        />
      </section>

      <section className={`${styles.section} ${styles.sectionCanvas}`} aria-labelledby="systems-features">
        <h2 className={styles.sectionTitle} id="systems-features">
          Everything your team needs to run it
        </h2>
        <FeatureGrid
          items={[
            { icon: <BarChart3 {...iconProps} />, title: "Analytics", body: "Bookings, call volume, and call duration, day by day." },
            { icon: <Users {...iconProps} />, title: "Staff activity", body: "See the work your team completes alongside the agent." },
            { icon: <BookOpen {...iconProps} />, title: "Knowledge base", body: "Keep the agent’s answers current, with a history of every change." },
            { icon: <ShieldCheck {...iconProps} />, title: "Insurance list", body: "Keep the plans your practice accepts current. The agent uses the same list." },
            { icon: <Bot {...iconProps} />, title: "Agent overview", body: "See what your agent does and the tools it can use." },
            { icon: <MapPin {...iconProps} />, title: "Locations", body: "Scope the work and the numbers by location." },
          ]}
        />
      </section>

      <PlatformTrio current="/platform/systems" />
      <CtaBand />
    </MarketingFrame>
  )
}

export function DeploymentPageContent() {
  return (
    <MarketingFrame current="/platform/deployment">
      <PageHero
        tone="dusk"
        art="rings"
        eyebrow="Deployment"
        title="Deployment is where practice AI succeeds or fails."
        lead="Our founders go on-site, build the agent around your workflow, and stay through rollout until your team owns it."
      />

      <section className={styles.section} aria-labelledby="deployment-team">
        <div className={styles.statRow}>
          <div>
            <h2 className={styles.sectionTitle} id="deployment-team">
              Embedded with your team
            </h2>
            <p className={styles.sectionLead}>
              The same people who build the agent sit with the staff who run the phones. Not a remote vendor: a team that
              stays involved from diagnosis through review.
            </p>
          </div>
          <p className={styles.stat}>
            <strong>80%</strong>
            <span>Calls fully contained by AI agents, per LiveKit’s case study.</span>
          </p>
        </div>
        <div className={styles.split}>
          <Atmosphere tone="dusk" shape="b" art="network" className={styles.splitCard}>
            <h3>
              Engineering <span>Makes it work.</span>
            </h3>
            <p>Builds the agent, connects it to your practice management system, and tests changes against real patient scenarios.</p>
          </Atmosphere>
          <div className={styles.splitCard}>
            <h3>
              Operations <span>Makes it matter.</span>
            </h3>
            <p>Redesigns the workflow with the people who run it, trains frontline staff, and manages the change.</p>
          </div>
        </div>
      </section>

      <section className={`${styles.section} ${styles.sectionCanvas}`} aria-labelledby="deployment-phases">
        <h2 className={styles.sectionTitle} id="deployment-phases">
          From Acuity-led to practice-owned, by design.
        </h2>
        <PhaseTabs
          phases={[
            {
              label: "Phase 01",
              title: "Baseline the work.",
              body: "Agree on volumes, failure modes, handoffs, and the KPIs that matter before anything is built.",
              tags: ["Call volumes", "Failure modes", "KPIs"],
            },
            {
              label: "Phase 02",
              title: "Redesign the workflow.",
              body: "Map the rules, exceptions, ownership, and new workflow with the people who run it.",
              tags: ["Scheduling rules", "Exceptions", "Ownership"],
            },
            {
              label: "Phase 03",
              title: "Test it in real operations.",
              body: "Launch a bounded pilot that tests the highest-risk assumptions where the work actually happens.",
              tags: ["Bounded pilot", "On-site launch", "Staff training"],
            },
            {
              label: "Phase 04",
              title: "Prove it, then expand.",
              body: "Compare results with the agreed KPIs, strengthen weak points, and expand when the evidence holds.",
              tags: ["KPI review", "Hardening", "Expansion"],
            },
          ]}
        />
      </section>

      <PlatformTrio current="/platform/deployment" />
      <CtaBand />
    </MarketingFrame>
  )
}

export function StrategyPageContent() {
  return (
    <MarketingFrame current="/platform/strategy">
      <PageHero
        tone="ember"
        art="slots"
        eyebrow="Strategy"
        title="Start with one workflow. Prove it. Then scale."
        lead="We help practices choose the patient-access work worth automating first, agree on what success means, and expand only when the evidence holds."
      />

      <section className={styles.section} aria-labelledby="strategy-capabilities">
        <h2 className={styles.sectionTitle} id="strategy-capabilities">
          Two capabilities make it work
        </h2>
        <div className={styles.split} data-testid="strategy-capabilities">
          <Atmosphere tone="dusk" shape="a" art="wave" className={styles.splitCard}>
            <h3>
              Agentic system design <span>The technology</span>
            </h3>
            <ul>
              <li>Data security + HIPAA safeguards</li>
              <li>Analytics + guardrails</li>
              <li>EMR integrations</li>
            </ul>
          </Atmosphere>
          <div className={styles.splitCard}>
            <h3>
              Workflow transformation <span>The operation</span>
            </h3>
            <ul>
              <li>Change management</li>
              <li>Workflow redesign</li>
              <li>Frontline training</li>
            </ul>
          </div>
        </div>
      </section>

      <section className={`${styles.section} ${styles.sectionCanvas}`} aria-labelledby="strategy-questions">
        <h2 className={styles.sectionTitle} id="strategy-questions">
          The questions we answer first
        </h2>
        <FeatureGrid
          items={[
            { icon: <BarChart3 {...iconProps} />, title: "Where is the volume?", body: "Which calls arrive most, and which ones fail or wait the longest." },
            { icon: <CalendarCheck {...iconProps} />, title: "What counts as success?", body: "The KPIs we agree on before we build, and how we will measure them." },
            { icon: <Inbox {...iconProps} />, title: "Who owns each handoff?", body: "What the agent finishes, what staff finish, and how work moves between them." },
          ]}
        />
      </section>

      <section className={styles.section} aria-labelledby="strategy-commitment">
        <div className={styles.twoColumn}>
          <h2 className={styles.sectionTitle} id="strategy-commitment">
            Prove it in the operation. Then earn the right to scale.
          </h2>
          <ul className={styles.checkList}>
            <li><Check size={18} aria-hidden="true" /> Founders remain involved from diagnosis through executive review.</li>
            <li><Check size={18} aria-hidden="true" /> We go on-site to understand frontline reality and support launch.</li>
            <li><Check size={18} aria-hidden="true" /> Expansion waits for performance, handoffs, adoption, and ownership.</li>
            <li><Check size={18} aria-hidden="true" /> The practice gains capability at every stage.</li>
          </ul>
        </div>
      </section>

      <PlatformTrio current="/platform/strategy" />
      <CtaBand />
    </MarketingFrame>
  )
}
