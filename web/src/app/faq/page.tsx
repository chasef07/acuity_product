import {
  CommercialPage,
  type CommercialPageSpec,
} from "@/components/marketing/commercial-page"
import { createPublicPageMetadata } from "@/lib/site"

const path = "/faq" as const
const description =
  "Answers about Acuity Health AI agents, patient-access workflows, AdvancedMD scheduling, staff handoffs, implementation, security review, and measurement."

export const metadata = createPublicPageMetadata({
  path,
  title: "Medical AI Receptionist FAQ",
  description,
})

const questions = [
  {
    question: "What does Acuity Health do?",
    answer:
      "Acuity deploys medical AI agents for patient access. The agents answer calls, complete approved administrative workflows in connected systems, and bring staff in when judgment, clinical responsibility, or an unsupported exception requires a person.",
  },
  {
    question: "Is Acuity only an AI answering service?",
    answer:
      "No. Answering the call is one interaction. Acuity is designed around the operational result: applying practice rules, completing supported work in the system of record, preserving evidence, and turning unresolved patient needs into accountable staff Tasks.",
  },
  {
    question: "Can Acuity schedule directly in AdvancedMD?",
    answer:
      "Acuity can complete supported appointment bookings in AdvancedMD under the practice’s configured provider, Location, appointment-type, insurance, availability, and escalation rules. A booking is represented as complete only after the connected action succeeds.",
  },
  {
    question: "Can Acuity handle ophthalmology and optometry workflows?",
    answer:
      "Yes, when the practice defines the approved boundaries. Acuity can map medical-versus-vision paths, visit types, providers, Locations, schedule constraints, prerequisites, and staff handoffs separately for ophthalmology and optometry.",
  },
  {
    question: "What happens when a caller needs clinical advice or urgent help?",
    answer:
      "Acuity does not diagnose or provide medical advice. It follows the practice’s approved escalation policy and keeps the patient need visible for the appropriate clinical or administrative owner. It does not replace emergency services.",
  },
  {
    question: "Does Acuity replace front-desk staff?",
    answer:
      "No. The operating goal is to return routine capacity to staff and give them better-supported exceptions. People retain responsibility for judgment, clinical questions, consequential decisions, and work outside the approved automation boundary.",
  },
  {
    question: "How is Acuity implemented?",
    answer:
      "Acuity starts with one bounded patient-access workflow. The team baselines the current operation, maps rules and failure states, configures integrations and handoffs, tests with the practice, launches under a defined release bar, and expands only after the outcome evidence is credible.",
  },
  {
    question: "How does Acuity measure success?",
    answer:
      "Success is measured at the workflow outcome, not by conversation volume alone. Depending on scope, that can include verified bookings, unresolved Tasks, transfers, staff intervention, dropped calls, patient effort, and estimated capacity. Estimates are labeled separately from recorded outcomes.",
  },
  {
    question: "How should a practice evaluate Acuity’s security and HIPAA posture?",
    answer:
      "Review the actual service scope, data flow, access model, agreements, subprocessors, retention, incident responsibilities, and customer obligations. Acuity supports a confidential security review. No website statement or vendor label substitutes for an executed agreement and deployment-specific review.",
  },
  {
    question: "How does an evaluation begin?",
    answer:
      "Bring one patient-access workflow and the operational problem it creates. Acuity will map the current failure, system actions, staff owners, evidence requirements, and the smallest safe deployment that can produce a measurable result.",
  },
] as const

const pageSpec: CommercialPageSpec = {
  route: path,
  schemaType: "WebPage",
  schemaName: "Medical AI Receptionist FAQ",
  eyebrow: "Medical AI receptionist FAQ",
  title: "Questions to answer before changing patient access.",
  description,
  trace: {
    eyebrow: "A useful evaluation",
    title: "Follow one patient need from first contact to real outcome.",
    steps: [
      {
        label: "Scope",
        title: "Choose one bounded workflow",
        description:
          "Define the patient need, the permitted actions, and the people responsible for exceptions.",
      },
      {
        label: "Rules",
        title: "Map the practice’s real constraints",
        description:
          "Provider, Location, appointment, insurance, system, and escalation rules become the operating specification.",
      },
      {
        label: "Failure",
        title: "Test what happens when work cannot complete",
        description:
          "Unavailable systems, missing information, ambiguity, and clinical questions must remain visible and recoverable.",
      },
      {
        label: "Evidence",
        title: "Verify the final state",
        description:
          "A conversation, click, or attempted action is not counted as a completed patient outcome.",
      },
    ],
  },
  details: [
    {
      eyebrow: "Product boundary",
      title: "Administrative automation with human responsibility intact.",
      description:
        "Acuity is built for patient-access and administrative work. Clinical judgment, ambiguous decisions, and consequential exceptions remain with qualified people.",
      bullets: [
        "Routine work follows practice-approved workflows",
        "Unsupported patient needs become visible staff Tasks",
        "Connected-system success establishes completion",
        "Clinical advice and emergency response stay outside the agent’s role",
      ],
    },
    {
      eyebrow: "Buying decision",
      title: "Compare the operation, not the demo voice.",
      description:
        "A polished conversation is useful only when the requested work reaches the correct system, evidence, owner, and next action.",
      bullets: [
        "Evaluate the same workflow and exception cases across vendors",
        "Confirm the source of truth and permitted system actions",
        "Review security and contractual scope for the actual deployment",
        "Require measurable outcomes before expanding",
      ],
    },
    {
      eyebrow: "Commercial next step",
      title: "Start with the costly failure already visible to the practice.",
      description:
        "The best first workflow has meaningful patient demand, a clear current failure, an accountable practice owner, and an outcome that both teams can verify.",
      bullets: [
        "Bring current call or scheduling failure data when available",
        "Identify the staff and system owners",
        "Define success and failure before configuration",
        "Keep the first deployment narrow enough to learn safely",
      ],
    },
  ],
  questions: [...questions],
  related: [
    {
      href: "/advancedmd-ai-receptionist",
      label: "AdvancedMD AI receptionist",
      description:
        "See how calls, practice rules, AdvancedMD actions, and evidence connect.",
    },
    {
      href: "/ai-receptionist-vs-medical-answering-service",
      label: "Compare operating models",
      description:
        "Decide when human-only coverage or workflow-completing AI is the better fit.",
    },
    {
      href: "/security",
      label: "Security, privacy, and HIPAA",
      description:
        "Review Acuity’s public security posture and the boundaries of that statement.",
    },
  ],
}

function FAQStructuredData() {
  const structuredData = {
    "@context": "https://schema.org",
    "@type": "FAQPage",
    "@id": `https://acuityhealth.io${path}#faq`,
    url: `https://acuityhealth.io${path}`,
    mainEntity: questions.map(({ question, answer }) => ({
      "@type": "Question",
      name: question,
      acceptedAnswer: {
        "@type": "Answer",
        text: answer,
      },
    })),
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

export default function FAQPage() {
  return (
    <>
      <FAQStructuredData />
      <CommercialPage spec={pageSpec} />
    </>
  )
}
