import {
  CommercialPage,
  type CommercialPageSpec,
} from "@/components/marketing/commercial-page"
import { ophthalmologyCaseStudyProof } from "@/lib/marketing-proof"
import { createPublicPageMetadata } from "@/lib/site"

const path = "/case-studies/ophthalmology-patient-access" as const
const description =
  "See how a six-location eye-care group reports moving from roughly 200 monthly dropped calls to zero in an operating snapshot with Acuity."

export const metadata = createPublicPageMetadata({
  path,
  title: "Ophthalmology Patient Access Case Study",
  description,
})

const pageSpec: CommercialPageSpec = {
  route: path,
  schemaType: "WebPage",
  schemaName: "Ophthalmology Patient Access Case Study",
  eyebrow: "Ophthalmology patient access case study",
  title: "From roughly 200 dropped calls a month to zero reported.",
  description,
  trace: {
    eyebrow: "Reported implementation path",
    title: "Map the operation, prove one Location, then expand.",
    steps: [
      {
        label: "Baseline",
        title: "Make missed patient demand visible",
        description:
          "The six-Location group reported approximately 200 dropped calls per month before Acuity and unused capacity in one OD schedule.",
      },
      {
        label: "Configure",
        title: "Translate practice workflows into rules",
        description:
          "Acuity spent approximately one month mapping scheduling, medical-versus-vision, Location, provider, and exception paths.",
      },
      {
        label: "Pilot",
        title: "Run one Location for one month",
        description:
          "The practice used a bounded pilot to review call handling, bookings, transfers, Tasks, and operating failures.",
      },
      {
        label: "Expand",
        title: "Roll out after the pilot",
        description:
          "The remaining five Locations were added after the pilot, for approximately two months from kickoff to full rollout.",
      },
    ],
  },
  proof: {
    eyebrow: `${ophthalmologyCaseStudyProof.scale} · reported operating results`,
    title: "More patient demand reached a documented next step.",
    description:
      "Bookings and dropped-call counts are reported operating outcomes. Staff time and recovered revenue are estimates; they are not independently audited or collected-revenue figures.",
    metrics: ophthalmologyCaseStudyProof.metrics,
  },
  details: [
    {
      eyebrow: "Practice context",
      title: "A multi-location eye-care operation with real scheduling complexity.",
      description:
        "The customer is an ophthalmology and optometry group with six Locations and eight providers. The deployment covered AI reception, direct scheduling, staff Tasks, and contextual transfer when judgment was required. The customer remains anonymous until stored publication approval is available.",
      bullets: [
        ophthalmologyCaseStudyProof.specialty,
        ophthalmologyCaseStudyProof.scale,
        ...ophthalmologyCaseStudyProof.operatingMetrics,
      ],
    },
    {
      eyebrow: "Implementation",
      title: "The rollout treated workflow design as part of the product.",
      description:
        "Acuity did not begin with a system-wide switch. The team mapped the practice, tested one Location, reviewed the operation, and expanded after the pilot.",
      bullets: [...ophthalmologyCaseStudyProof.implementation],
    },
    {
      eyebrow: "Measurement method",
      title: "Recorded outcomes and modeled impact are kept separate.",
      description:
        "The case study distinguishes operational counts from estimates so a buyer can evaluate what happened without treating every number as the same evidence class.",
      bullets: [...ophthalmologyCaseStudyProof.methodology],
    },
    {
      eyebrow: "Limitations",
      title: "One customer result is evidence, not a universal promise.",
      description:
        "Practice mix, patient demand, workflow rules, staffing, systems, and implementation quality all affect results. Acuity scopes a new baseline rather than projecting this outcome onto another practice.",
      bullets: [...ophthalmologyCaseStudyProof.limitations],
    },
  ],
  comparison: {
    eyebrow: "Reported before and after",
    title: "Two operational constraints changed after rollout.",
    firstColumn: "Before Acuity",
    secondColumn: "Reported after rollout",
    rows: [
      {
        label: "Dropped calls",
        first: "Approximately 200 per month across six Locations.",
        second: "Zero reported in the operating snapshot.",
      },
      {
        label: "Selected OD schedule",
        first: "Approximately 50% booked at one office.",
        second: "Approximately 90% booked at that office.",
      },
    ],
  },
  questions: [
    {
      question: "Are these results independently audited?",
      answer:
        "No. They are customer- and management-reported operating results. Acuity labels estimated staff time and recovered revenue separately and does not present this page as an independent audit.",
    },
    {
      question: "How was staff time returned estimated?",
      answer:
        "The estimate is based on the minutes of patient calls handled by Acuity. It represents modeled staff capacity, not a payroll reduction or guarantee that every minute was redeployed.",
    },
    {
      question: "How was recovered revenue estimated?",
      answer:
        "The estimate uses previously dropped-call volume and booking-value assumptions. It is directional operating impact, not collected revenue, audited financial performance, or a guaranteed result.",
    },
    {
      question: "What did the AI resolve without staff?",
      answer:
        "The practice reports that 70% of inbound calls were resolved without transfer and 30% transferred to staff. The exact resolution boundary depends on the approved workflow and supporting system action.",
    },
    {
      question: "Will another ophthalmology practice get the same result?",
      answer:
        "Not necessarily. Acuity baselines each practice’s patient demand, rules, systems, staff model, and current failure modes. This case study shows one operating path, not a forecast or guarantee.",
    },
  ],
  related: [
    {
      href: "/ai-receptionist-for-ophthalmology",
      label: "AI receptionist for ophthalmology",
      description:
        "See the specialty rules, system actions, and staff handoffs behind the operating model.",
    },
    {
      href: "/method",
      label: "The Acuity Health Method",
      description:
        "See how Acuity baselines, redesigns, validates, and expands patient-access work.",
    },
    {
      href: "/faq",
      label: "Medical AI receptionist FAQ",
      description:
        "Review the product, workflow, implementation, security, and measurement questions buyers ask.",
    },
  ],
}

export default function OphthalmologyPatientAccessCaseStudyPage() {
  return <CommercialPage spec={pageSpec} />
}
