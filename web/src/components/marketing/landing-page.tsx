"use client"

import { ArrowRight, Mail, MessageSquareText } from "lucide-react"
import Image from "next/image"
import Link from "next/link"
import { type CSSProperties, useEffect, useRef, useState } from "react"

import { AcuityMark as AcuityHealthMark } from "@/components/acuity-mark"
import {
  PortalSignInTrigger,
  SignInDialog,
} from "@/components/auth/sign-in-dialog"
import { cn } from "@/lib/utils"

import styles from "./landing-page.module.css"

const journeyEvents = [
  {
    kind: "voice",
    source: "Voice agent",
    detail: "Books the appointment",
    scatter: [10, 25],
    route: [10, 42],
    rotate: -5,
  },
  {
    kind: "insurance",
    source: "Insurance agent",
    detail: "Verifies coverage",
    scatter: [50, 25],
    route: [30, 36],
    rotate: 4,
  },
  {
    kind: "reminder",
    source: "Workflow agent",
    detail: "Sends the reminder",
    scatter: [84, 68],
    route: [50, 44],
    rotate: 4,
  },
  {
    kind: "recovery",
    source: "EMR agent",
    detail: "Detects the no-show",
    scatter: [50, 76],
    route: [70, 35],
    rotate: -4,
  },
  {
    kind: "task",
    source: "Staff task",
    detail: "Maya reviews and sends",
    scatter: [15, 72],
    route: [90, 39],
    rotate: 3,
  },
] as const

const journeyRoutePath =
  "M100 210 C175 150 235 145 300 180 C370 220 430 250 500 220 C565 190 630 145 700 175 C770 205 825 220 900 195"

const heroTasks = [
  { kind: "voice", source: "Voice agent", detail: null },
  { kind: "insurance", source: "Insurance agent", detail: null },
  { kind: "task", source: "Task agent", detail: null },
] as const

const workStackSteps = [
  "Channels",
  "Signals",
  "Work gets done",
  "Finished work",
] as const

type JourneyKind = (typeof journeyEvents)[number]["kind"]

function useScrollStep(stepCount: number, revealStart: number) {
  const sectionRef = useRef<HTMLElement>(null)
  const [activeStep, setActiveStep] = useState(-1)

  useEffect(() => {
    let animationFrame = 0

    const update = () => {
      const section = sectionRef.current
      if (!section) return

      const rect = section.getBoundingClientRect()
      const scrollRange = Math.max(rect.height - window.innerHeight, 1)
      const progress = Math.min(Math.max(-rect.top / scrollRange, 0), 1)
      const nextStep =
        progress < revealStart
          ? -1
          : Math.min(
              stepCount - 1,
              Math.floor(
                ((progress - revealStart) / (1 - revealStart)) * stepCount,
              ),
            )

      setActiveStep((current) => (current === nextStep ? current : nextStep))
    }

    const queueUpdate = () => {
      cancelAnimationFrame(animationFrame)
      animationFrame = requestAnimationFrame(update)
    }

    update()
    window.addEventListener("scroll", queueUpdate, { passive: true })
    window.addEventListener("resize", queueUpdate)
    return () => {
      cancelAnimationFrame(animationFrame)
      window.removeEventListener("scroll", queueUpdate)
      window.removeEventListener("resize", queueUpdate)
    }
  }, [revealStart, stepCount])

  return [sectionRef, activeStep] as const
}

function HeroTaskGlyph({ kind }: { kind: (typeof heroTasks)[number]["kind"] }) {
  if (kind === "voice") {
    return (
      <span className={styles.heroVoiceGlyph}>
        {Array.from({ length: 11 }).map((_, index) => (
          <i key={index} />
        ))}
      </span>
    )
  }

  if (kind === "insurance") {
    return (
      <span className={styles.heroInsuranceGlyph}>
        <i />
        <i />
        <i />
        <b />
      </span>
    )
  }

  return (
    <span className={styles.heroTaskGlyph}>
      <i />
      <i />
      <i />
    </span>
  )
}

function AcuityMark() {
  return (
    <span className={styles.brandMark} aria-hidden="true">
      <AcuityHealthMark className={styles.brandGlyph} />
    </span>
  )
}

function JourneyObject({ kind }: { kind: JourneyKind }) {
  if (kind === "voice") {
    return (
      <div className={cn(styles.journeyObject, styles.voiceObject)}>
        <span className={styles.liveDot} />
        <span className={styles.journeyWave} aria-hidden="true">
          {Array.from({ length: 15 }).map((_, index) => (
            <i key={index} />
          ))}
        </span>
        <small>Appointment booked · Jun 18</small>
      </div>
    )
  }

  if (kind === "insurance") {
    return (
      <div className={cn(styles.journeyObject, styles.insurancePage)}>
        <small>Eligibility</small>
        <i />
        <i />
        <i />
        <b>Active</b>
        <span />
      </div>
    )
  }

  if (kind === "reminder") {
    return (
      <div className={cn(styles.journeyObject, styles.reminderMessage)}>
        <MessageSquareText size={14} aria-hidden="true" />
        <p>Your visit is tomorrow at 10:30 AM.</p>
      </div>
    )
  }

  if (kind === "recovery") {
    return (
      <div className={cn(styles.journeyObject, styles.recoveryCalendar)}>
        <small>EMR · Schedule</small>
        <span className={styles.recoveryAppointment}>
          <b>10:30</b>
          <span>
            <strong>Jordan Lee</strong>
            <i>Follow-up visit</i>
          </span>
          <em>No-show</em>
        </span>
      </div>
    )
  }

  return (
    <div className={cn(styles.journeyObject, styles.preparedTask)}>
      <strong>Send medical records</strong>
      <span>
        <b>M</b> Maya · Ready
      </span>
    </div>
  )
}

const caseMetrics = [
  { value: "500+", label: "appointments booked into the EMR" },
  { value: "300", label: "tasks created and completed" },
  { value: "0", label: "missed calls in 30 days" },
  { value: "500", label: "staff hours returned" },
  { value: "$100K+", label: "estimated revenue recovered" },
] as const

export function LandingPage({
  googleEnabled,
  initiallyOpen = false,
}: {
  googleEnabled: boolean
  initiallyOpen?: boolean
}) {
  const [workflowRef, activeWorkflowStep] = useScrollStep(
    journeyEvents.length,
    0.045,
  )
  const [workStackRef, visibleStackStep] = useScrollStep(
    workStackSteps.length,
    0.04,
  )

  const workflowStep =
    activeWorkflowStep >= 0 ? journeyEvents[activeWorkflowStep] : null
  const routeProgress =
    activeWorkflowStep < 0
      ? 0
      : ((activeWorkflowStep + 1) / journeyEvents.length) * 100

  return (
    <SignInDialog googleEnabled={googleEnabled} initiallyOpen={initiallyOpen}>
      <main className={styles.page}>
        <header className={styles.header}>
          <Link
            className={styles.brand}
            href="/"
            aria-label="Acuity Health home"
          >
            <AcuityMark />
            <span>Acuity</span>
          </Link>
          <nav className={styles.nav} aria-label="Main navigation">
            <a href="#patient-work">Patient journey</a>
            <a href="#results">Results</a>
            <PortalSignInTrigger />
          </nav>
        </header>

        <section className={styles.hero}>
          <h1>
            Do <span className={styles.heroEmphasis}>more</span> patient work
            <br />
            with the same team.
          </h1>
          <p className={styles.heroBody}>
            Acuity answers calls, completes patient work, and brings in your
            team when needed.
          </p>
          <div
            className={styles.heroTaskStage}
            aria-label="Acuity agents connected across patient work"
          >
            <svg
              className={styles.heroWorkflowRoute}
              viewBox="0 0 1180 140"
              preserveAspectRatio="none"
              aria-hidden="true"
            >
              <defs>
                <clipPath id="hero-route-reveal">
                  <rect
                    className={styles.heroRouteReveal}
                    x="0"
                    y="0"
                    width="1180"
                    height="140"
                  />
                </clipPath>
              </defs>
              <g clipPath="url(#hero-route-reveal)">
                <path
                  className={styles.heroRouteBase}
                  pathLength="1"
                  d="M190 70 C300 10 440 124 540 70"
                />
                <path
                  className={styles.heroRouteBase}
                  pathLength="1"
                  d="M640 70 C750 16 890 122 990 70"
                />
              </g>
            </svg>
            <div className={styles.heroAgentJourney}>
              {heroTasks.map((task) => (
                <div className={styles.heroAgentStop} key={task.kind}>
                  <span className={styles.heroTaskVisual} aria-hidden="true">
                    <HeroTaskGlyph kind={task.kind} />
                  </span>
                  <span className={styles.heroTaskCopy}>
                    <small>{task.source}</small>
                  </span>
                </div>
              ))}
            </div>
          </div>
        </section>

        <section className={styles.thesis}>
          <h2>
            Most systems capture the signal.{" "}
            <em>Acuity carries out the work.</em>
          </h2>
        </section>

        <section
          className={styles.fragmentation}
          id="patient-work"
          ref={workflowRef}
        >
          <div className={styles.workflowSticky}>
            <div className={styles.fragmentationCopy}>
              <h2>
                <span>Your patients have one journey.</span>
                <span>Your team has five tabs.</span>
              </h2>
            </div>
            <div
              className={cn(
                styles.journeyCanvas,
                activeWorkflowStep >= 0 && styles.journeyCanvasConnected,
              )}
              aria-label={
                workflowStep
                  ? `${workflowStep.source}. ${workflowStep.detail}.`
                  : "Patient work scattered across disconnected systems"
              }
              tabIndex={0}
            >
              <span
                className={styles.journeyWord}
                key={activeWorkflowStep >= 0 ? "journey" : "tabs"}
                aria-hidden="true"
              >
                {activeWorkflowStep >= 0 ? "One journey." : "Five tabs."}
              </span>
              <svg
                className={styles.journeyRoute}
                viewBox="0 0 1000 500"
                preserveAspectRatio="none"
                aria-hidden="true"
              >
                <defs>
                  <mask id="journey-progress-mask">
                    <path
                      className={styles.routeMask}
                      d={journeyRoutePath}
                      pathLength="100"
                      style={{ strokeDashoffset: 100 - routeProgress }}
                    />
                  </mask>
                </defs>
                <path
                  className={styles.routeBase}
                  d={journeyRoutePath}
                  pathLength="100"
                />
                <path
                  className={styles.routeProgress}
                  d={journeyRoutePath}
                  pathLength="100"
                  mask="url(#journey-progress-mask)"
                />
              </svg>
              {activeWorkflowStep >= 0 && (
                <span
                  className={styles.routePulse}
                  style={
                    {
                      "--pulse-x": `${journeyEvents[activeWorkflowStep].route[0]}%`,
                      "--pulse-y": `${journeyEvents[activeWorkflowStep].route[1]}%`,
                    } as CSSProperties
                  }
                  aria-hidden="true"
                />
              )}
              {journeyEvents.map(
                ({ kind, source, detail, scatter, route, rotate }, index) => {
                  const workflowState =
                    activeWorkflowStep < 0
                      ? ""
                      : index === activeWorkflowStep
                        ? styles.journeyCurrent
                        : index < activeWorkflowStep
                          ? styles.journeyComplete
                          : styles.journeyFuture

                  return (
                    <article
                      className={cn(
                        styles.journeyPiece,
                        styles[`journey${kind}`],
                        workflowState,
                      )}
                      key={`${source}-${detail}`}
                      style={
                        {
                          "--scatter-x": `${scatter[0]}%`,
                          "--scatter-y": `${scatter[1]}%`,
                          "--route-x": `${route[0]}%`,
                          "--route-y": `${route[1]}%`,
                          "--journey-rotate": `${rotate}deg`,
                        } as CSSProperties
                      }
                    >
                      <JourneyObject kind={kind} />
                      <span className={styles.journeyCaption}>
                        <small>{source}</small>
                        <strong>{detail}</strong>
                      </span>
                    </article>
                  )
                },
              )}
            </div>
          </div>
        </section>

        <section
          className={styles.workStackSection}
          id="how-it-works"
          ref={workStackRef}
        >
          <div className={styles.workStackSticky}>
            <header className={styles.workStackHeader}>
              <div>
                <p className={styles.sectionLabel}>How Acuity works</p>
                <h2>From every signal to finished work.</h2>
              </div>
              <span className={styles.stackStepReadout} aria-live="polite">
                {visibleStackStep < 0
                  ? "The complete model"
                  : workStackSteps[visibleStackStep]}
              </span>
            </header>

            <div
              className={cn(
                styles.workStackStage,
                visibleStackStep >= 0 && styles.stackIsFocused,
              )}
              aria-label="Acuity patient work model"
            >
              <span className={styles.stackSpine} aria-hidden="true">
                <i
                  style={
                    {
                      "--stack-progress":
                        visibleStackStep < 0
                          ? 0
                          : visibleStackStep / (workStackSteps.length - 1),
                    } as CSSProperties
                  }
                />
              </span>

              <article
                className={cn(
                  styles.stackPlane,
                  styles.stackChannels,
                  visibleStackStep === 0 && styles.stackPlaneActive,
                )}
              >
                <div className={styles.stackPlanePreview}>
                  <small>Channels</small>
                  <strong>
                    Calls · Voicemails · Texts · Emails · Referrals · EMR
                  </strong>
                </div>
                <div
                  className={styles.stackPlaneDetail}
                  data-testid="work-stack-detail"
                  tabIndex={visibleStackStep === 0 ? 0 : -1}
                >
                  <div className={styles.stackDetailHeading}>
                    <small>Channels</small>
                    <strong>Patient work starts everywhere.</strong>
                  </div>
                  <div className={styles.channelArtifactRail}>
                    <svg
                      className={styles.channelConstellation}
                      viewBox="0 0 1000 260"
                      preserveAspectRatio="none"
                      aria-hidden="true"
                    >
                      <path d="M70 78 C170 22 250 128 360 75 S560 20 650 82 S835 145 930 64" />
                      <path d="M165 210 C300 150 420 226 555 184 S745 132 880 205" />
                    </svg>
                    <div
                      className={cn(styles.channelArtifact, styles.channelCall)}
                    >
                      <span className={styles.channelVisual} aria-hidden="true">
                        <span className={styles.stackWave}>
                          {Array.from({ length: 9 }).map((_, index) => (
                            <i key={index} />
                          ))}
                        </span>
                      </span>
                      <strong>Calls</strong>
                    </div>
                    <div
                      className={cn(
                        styles.channelArtifact,
                        styles.channelVoicemail,
                      )}
                    >
                      <span className={styles.channelVisual} aria-hidden="true">
                        <span className={styles.voicemailMark}>0:18</span>
                      </span>
                      <strong>Voicemails</strong>
                    </div>
                    <div
                      className={cn(styles.channelArtifact, styles.channelText)}
                    >
                      <span className={styles.channelVisual} aria-hidden="true">
                        <MessageSquareText />
                      </span>
                      <strong>Texts</strong>
                    </div>
                    <div
                      className={cn(
                        styles.channelArtifact,
                        styles.channelEmail,
                      )}
                    >
                      <span className={styles.channelVisual} aria-hidden="true">
                        <Mail />
                      </span>
                      <strong>Emails</strong>
                    </div>
                    <div
                      className={cn(
                        styles.channelArtifact,
                        styles.channelReferral,
                      )}
                    >
                      <span className={styles.channelVisual} aria-hidden="true">
                        <span className={styles.referralMini}>
                          <small>FAX</small>
                          <i />
                          <i />
                          <i />
                        </span>
                      </span>
                      <strong>Referrals</strong>
                    </div>
                    <div
                      className={cn(styles.channelArtifact, styles.channelEmr)}
                    >
                      <span className={styles.channelVisual} aria-hidden="true">
                        <span className={styles.emrMini}>
                          <i />
                          <b>
                            <em />
                            <em />
                          </b>
                        </span>
                      </span>
                      <strong>EMR updates</strong>
                    </div>
                  </div>
                </div>
              </article>

              <article
                className={cn(
                  styles.stackPlane,
                  styles.stackSignals,
                  visibleStackStep === 1 && styles.stackPlaneActive,
                )}
              >
                <div className={styles.stackPlanePreview}>
                  <small>Signals</small>
                  <strong>
                    Requested · Changed · Missing · Needs attention
                  </strong>
                </div>
                <div
                  className={styles.stackPlaneDetail}
                  data-testid="work-stack-detail"
                  tabIndex={visibleStackStep === 1 ? 0 : -1}
                >
                  <div className={styles.stackDetailHeading}>
                    <small>Signals</small>
                    <strong>Acuity understands what each signal means.</strong>
                  </div>
                  <div className={styles.signalExamples}>
                    <article>
                      <span
                        className={styles.signalMessageMark}
                        aria-hidden="true"
                      >
                        <MessageSquareText size={22} />
                      </span>
                      <small>Requested</small>
                      <strong>A patient asks to move a visit.</strong>
                    </article>
                    <article>
                      <span
                        className={styles.signalCalendarMark}
                        aria-hidden="true"
                      >
                        <i />
                        <b>12</b>
                      </span>
                      <small>Changed</small>
                      <strong>The EMR reports a no-show.</strong>
                    </article>
                    <article>
                      <span
                        className={styles.signalDocumentMark}
                        aria-hidden="true"
                      >
                        <i />
                        <i />
                        <i />
                      </span>
                      <small>Missing</small>
                      <strong>A referral arrives without insurance.</strong>
                    </article>
                    <article>
                      <span
                        className={styles.signalAttentionMark}
                        aria-hidden="true"
                      />
                      <small>Needs attention</small>
                      <strong>A refill requires staff approval.</strong>
                    </article>
                  </div>
                </div>
              </article>

              <article
                className={cn(
                  styles.stackPlane,
                  styles.stackExecution,
                  visibleStackStep === 2 && styles.stackPlaneActive,
                )}
              >
                <div className={styles.stackPlanePreview}>
                  <small>Work gets done</small>
                  <strong>
                    Reschedule · Recover · Verify · Route · Follow through
                  </strong>
                </div>
                <div
                  className={styles.stackPlaneDetail}
                  data-testid="work-stack-detail"
                  tabIndex={visibleStackStep === 2 ? 0 : -1}
                >
                  <div className={styles.stackDetailHeading}>
                    <small>Work gets done</small>
                    <strong>
                      Acuity completes the work behind every signal.
                    </strong>
                  </div>
                  <div className={styles.completionExamples}>
                    <article>
                      <div
                        className={cn(
                          styles.completionArtifact,
                          styles.completionBooking,
                        )}
                        aria-hidden="true"
                      >
                        <span>
                          <small>Jun</small>
                          <b>18</b>
                        </span>
                        <i>10:30 AM</i>
                        <em>Booked</em>
                      </div>
                      <strong>Appointment booked in EMR</strong>
                    </article>
                    <article>
                      <div
                        className={cn(
                          styles.completionArtifact,
                          styles.completionCall,
                        )}
                        aria-hidden="true"
                      >
                        <span>
                          {Array.from({ length: 11 }).map((_, index) => (
                            <i key={index} />
                          ))}
                        </span>
                        <small>Connected · 01:42</small>
                      </div>
                      <strong>Patient called and rebooked</strong>
                    </article>
                    <article>
                      <div
                        className={cn(
                          styles.completionArtifact,
                          styles.completionInsurance,
                        )}
                        aria-hidden="true"
                      >
                        <small>Eligibility</small>
                        <i />
                        <i />
                        <i />
                        <span />
                        <em>Active</em>
                      </div>
                      <strong>Insurance verified</strong>
                    </article>
                    <article>
                      <div
                        className={cn(
                          styles.completionArtifact,
                          styles.completionTask,
                        )}
                        aria-hidden="true"
                      >
                        <small>Created by Acuity</small>
                        <b>Send medical records</b>
                        <span>
                          <i>M</i>Maya · Ready
                        </span>
                      </div>
                      <strong>Task created for staff</strong>
                    </article>
                  </div>
                </div>
              </article>

              <article
                className={cn(
                  styles.stackPlane,
                  styles.stackResolution,
                  visibleStackStep === 3 && styles.stackPlaneActive,
                )}
              >
                <div className={styles.stackPlanePreview}>
                  <small>Finished work</small>
                  <strong>The patient is ready · The loop is closed</strong>
                </div>
                <div
                  className={styles.stackPlaneDetail}
                  data-testid="work-stack-detail"
                  tabIndex={visibleStackStep === 3 ? 0 : -1}
                >
                  <div className={styles.resolutionStory}>
                    <div>
                      <small>Finished work</small>
                      <strong>The patient is ready. The loop is closed.</strong>
                    </div>
                    <figure>
                      <Image
                        src="/marketing/visit-confirmed.png"
                        alt="A patient arriving for her scheduled visit"
                        fill
                        sizes="(max-width: 640px) 100vw, 48vw"
                      />
                    </figure>
                  </div>
                </div>
              </article>
            </div>
          </div>
        </section>

        <section className={styles.caseStudy} id="results">
          <div className={styles.caseStudyInner}>
            <p>Six-location ophthalmology group / first 30 days</p>
            <h2>Acuity saved 500 hours of patient work in 30 days.</h2>
            <dl className={styles.caseMetrics}>
              {caseMetrics.map((metric) => (
                <div key={metric.label}>
                  <dt>{metric.value}</dt>
                  <dd>{metric.label}</dd>
                </div>
              ))}
            </dl>
          </div>
        </section>

        <section className={styles.capacitySection}>
          <p>
            More capacity should not mean more tabs, more sticky notes, or more
            staff chasing work between systems.
          </p>
          <h2>Do more for patients with the team you already have.</h2>
          <a className={styles.lightButton} href="#patient-work">
            See how the work moves <ArrowRight size={17} />
          </a>
        </section>

        <footer className={styles.footer}>
          <Link
            className={styles.brand}
            href="/"
            aria-label="Acuity Health home"
          >
            <AcuityMark />
            <span>Acuity</span>
          </Link>
          <p>AI agents for patient management.</p>
          <Link href="/sign-in">Portal sign in</Link>
        </footer>
      </main>
    </SignInDialog>
  )
}
