import {
  CommercialPage,
  type CommercialPageSpec,
} from "@/components/marketing/commercial-page"
import { createPublicPageMetadata } from "@/lib/site"

const description =
  "Acuity Health helps AdvancedMD practices answer calls, book supported appointments, apply practice rules, and route exceptions with evidence and ownership."

export const metadata = createPublicPageMetadata({
  path: "/advancedmd-ai-receptionist",
  title: "AdvancedMD AI Receptionist for Practices",
  description,
})

const pageSpec: CommercialPageSpec = {
  route: "/advancedmd-ai-receptionist",
  schemaType: "Service",
  schemaName: "AdvancedMD AI Receptionist",
  eyebrow: "AdvancedMD integration",
  title: "An AdvancedMD AI receptionist built to complete the work.",
  description,
  trace: {
    eyebrow: "From call to accountable outcome",
    title:
      "A completed call is not enough. The work has to reach AdvancedMD or a clear owner.",
    steps: [
      {
        label: "Inbound signal",
        title: "Understand the stated need",
        description:
          "The agent captures the caller's need and available context without treating a phone number or name as verified identity.",
      },
      {
        label: "Practice rules",
        title: "Apply the real constraints",
        description:
          "Configured provider, appointment, location, insurance, and escalation rules determine whether the patient need can proceed.",
      },
      {
        label: "AdvancedMD action",
        title: "Commit supported work",
        description:
          "Eligible appointments are booked in AdvancedMD only after the supported action succeeds.",
      },
      {
        label: "Evidence + owner",
        title: "Keep the outcome accountable",
        description:
          "The outcome keeps its supporting evidence. Anything unresolved becomes organized staff work with context and a clear next step.",
      },
    ],
  },
  details: [
    {
      eyebrow: "Inbound patient access",
      title: "Handle demand without turning every call into another message.",
      description:
        "Acuity answers common, approved questions and completes supported scheduling work while leaving consequential decisions to staff.",
      bullets: [
        "Answer inbound calls using practice-approved information",
        "Capture after-hours demand and preserve the patient need for follow-up",
        "Book supported appointment types directly into AdvancedMD",
        "Route calls marked urgent under configured escalation rules without claiming clinical triage",
      ],
    },
    {
      eyebrow: "Practice-specific rules",
      title: "Match the workflow to the practice, not the other way around.",
      description:
        "Deployment starts with the rules that actually control scheduling and follow-up in each Location.",
      bullets: [
        "Map appointment types, providers, locations, hours, and prerequisites",
        "Apply configured scheduling, insurance, and location-specific workflow rules",
        "Keep unsupported or ambiguous patient needs with people",
        "Validate one workflow and its failure states before expanding",
      ],
    },
    {
      eyebrow: "System of record",
      title: "Keep AdvancedMD authoritative and exceptions accountable.",
      description:
        "AdvancedMD remains the source of truth for practice data and supported scheduling actions. Acuity carries the context and responsibility around unresolved work.",
      bullets: [
        "Commit only supported actions backed by successful integration evidence",
        "Do not represent a fluent conversation as proof of a completed booking",
        "Give staff the caller's need, known context, next action, and ownership",
        "Preserve failure and handoff evidence so unresolved patient work stays visible",
      ],
    },
  ],
  comparison: {
    eyebrow: "Why integration depth matters",
    title: "A phone answer is not the same as an operational outcome.",
    firstColumn: "Generic answering layer",
    secondColumn: "Acuity with AdvancedMD",
    rows: [
      {
        label: "Appointment needs",
        first: "Captures a message or passes the call onward.",
        second:
          "Completes supported bookings in AdvancedMD under practice rules.",
      },
      {
        label: "Practice complexity",
        first: "Relies on a broad script.",
        second:
          "Maps provider, location, appointment, insurance, and escalation constraints.",
      },
      {
        label: "Exceptions",
        first: "Ends at a transfer or note.",
        second:
          "Routes unresolved work with context, an owner, and a clear next step.",
      },
      {
        label: "Proof",
        first: "Measures answered calls.",
        second:
          "Separates conversation activity from evidence of a completed action.",
      },
    ],
  },
  validation: {
    eyebrow: "Independent marketplace validation",
    title: "Acuity Health is listed in the AdvancedMD Marketplace.",
    description:
      "Review AdvancedMD’s public Acuity Health listing for an external description of the partnership, supported workflows, and system-of-record relationship.",
    href: "https://www.advancedmd.com/integrations/marketplace/acuity-health/",
    label: "View the AdvancedMD listing",
  },
  questions: [
    {
      question: "Is this an AI phone agent that integrates with AdvancedMD?",
      answer:
        "Yes. Acuity answers inbound calls and can complete supported appointment bookings in AdvancedMD under the practice's configured rules. We validate each workflow's exact actions and failure states before expanding.",
    },
    {
      question:
        "How is this different from an EMR-integrated answering service?",
      answer:
        "An answering service may capture a message or transfer a call. Acuity is designed to apply practice rules, complete supported actions in AdvancedMD, and make unresolved work accountable to staff.",
    },
    {
      question: "What happens when the AI agent cannot resolve a patient need?",
      answer:
        "It does not mark the work complete. The unresolved patient need becomes a Task for staff with the available context, a clear next action, and accountable ownership.",
    },
    {
      question: "Can it handle after-hours or urgent calls?",
      answer:
        "Acuity can capture after-hours demand and route calls marked urgent according to configured practice rules. It does not replace clinical judgment, emergency services, or the practice's clinical escalation policy.",
    },
    {
      question: "Does every AdvancedMD practice use the same workflow?",
      answer:
        "No. Provider templates, appointment types, locations, insurance requirements, and handoffs vary. The deployment is mapped to the practice's actual operating rules.",
    },
    {
      question: "How does an evaluation start?",
      answer:
        "Bring one patient-access workflow to a working session. We map its rules, AdvancedMD actions, exception owners, and required evidence before defining a deployment scope.",
    },
  ],
  related: [
    {
      href: "/method",
      label: "The Acuity Health Method",
      description:
        "See how one workflow is baselined, redesigned, validated, and expanded.",
    },
    {
      href: "/ai-receptionist-for-ophthalmology",
      label: "AI receptionist for ophthalmology",
      description:
        "See how the operating model adapts to specialty scheduling and patient access.",
    },
    {
      href: "/ai-receptionist-vs-medical-answering-service",
      label: "AI vs. medical answering service",
      description:
        "Compare message capture with workflow completion and accountable exception handling.",
    },
  ],
}

export default function AdvancedMDAIReceptionistPage() {
  return <CommercialPage spec={pageSpec} />
}
