import {
  CommercialPage,
  type CommercialPageSpec,
} from "@/components/marketing/commercial-page"
import { createPublicPageMetadata } from "@/lib/site"

const path = "/ai-receptionist-vs-medical-answering-service" as const

export const metadata = createPublicPageMetadata({
  path,
  title: "AI Receptionist vs Answering Service",
  description:
    "Compare AI receptionists and medical answering services across workflow completion, system integration, handoffs, oversight, and verified outcome evidence.",
})

const pageSpec: CommercialPageSpec = {
  route: path,
  schemaType: "WebPage",
  schemaName: "AI Receptionist vs Medical Answering Service",
  eyebrow: "Operating model comparison",
  title:
    "AI receptionist vs. medical answering service: choose by the work you need done.",
  description:
    "A traditional medical answering service can be the right choice for human-only coverage or message capture. Acuity is designed for practices that want approved workflows completed, updates recorded in the systems their teams use, explicit handoffs, and evidence of the outcome.",
  trace: {
    eyebrow: "The Acuity operating model",
    title: "A patient need should move toward an outcome, not just an inbox.",
    steps: [
      {
        label: "Understand",
        title: "Identify the patient need and the rules that apply",
        description:
          "The agent gathers the information required for the approved workflow without treating a conversation as verified identity or clinical truth.",
      },
      {
        label: "Execute",
        title: "Complete the permitted operational steps",
        description:
          "Defined actions can be carried through in connected systems when the required conditions and evidence are present.",
      },
      {
        label: "Handoff",
        title: "Make exceptions visible to the right person",
        description:
          "Unresolved or consequential work is handed to staff with context, ownership, and a clear next action.",
      },
      {
        label: "Verify",
        title: "Record what actually happened",
        description:
          "The operating record distinguishes a completed outcome from an attempt, message, or unsupported claim of success.",
      },
    ],
  },
  details: [
    {
      eyebrow: "Start with the job",
      title: "Decide whether you need coverage, completion, or both.",
      description:
        "The category label matters less than the operating result. Define the patient need, the action that resolves it, and what staff should receive when it cannot be resolved.",
      bullets: [
        "Choose human-only coverage when a person answering and capturing a reliable message is the desired service.",
        "Consider an AI operating model when repeatable patient needs should move through approved rules and connected systems.",
        "Keep clinical judgment, ambiguous decisions, and consequential exceptions with qualified staff.",
        "Require a visible owner and next action whenever the patient need remains unresolved.",
      ],
    },
    {
      eyebrow: "Evaluate the operation",
      title: "Test the complete workflow, not a polished conversation.",
      description:
        "A natural-sounding call does not prove that the needed work reached the right system or outcome. Evaluation should follow the patient need from first contact through execution, exception handling, and verification.",
      bullets: [
        "Use real workflow rules, hours, locations, scheduling constraints, and escalation paths.",
        "Confirm which system is the source of truth and what evidence establishes a successful action.",
        "Test unavailable systems, incomplete information, ambiguous patient needs, and failed handoffs before launch.",
        "Measure resolved patient needs, unresolved work, staff intervention, patient effort, and verified outcomes.",
      ],
    },
    {
      eyebrow: "Know the tradeoff",
      title: "Automation increases the need for precise boundaries and evidence.",
      description:
        "Completing more work can reduce manual coordination, but only when permissions, fallbacks, oversight, and failure states are designed into the operation. Those controls should be explicit before volume increases.",
      bullets: [
        "Document what the agent may do, what it must never do, and when a person takes over.",
        "Review privacy, security, access, retention, and vendor obligations for the actual deployment.",
        "Monitor tool execution and final state rather than treating fluent dialogue as proof of completion.",
        "Expand only after one bounded workflow meets an agreed operational release bar.",
      ],
    },
  ],
  comparison: {
    eyebrow: "Side-by-side",
    title: "Two useful models built for different jobs.",
    firstColumn: "Traditional medical answering service",
    secondColumn: "Acuity AI operating model",
    rows: [
      {
        label: "Primary job",
        first: "Provide human coverage and capture or relay messages according to the service scope.",
        second:
          "Move defined patient needs through approved workflows and preserve the resulting evidence.",
      },
      {
        label: "System interaction",
        first:
          "Often centered on call handling and message delivery; available system access varies by provider and contract.",
        second:
          "Designed around scoped connections to the systems where permitted operational work is completed.",
      },
      {
        label: "Exceptions",
        first:
          "Handled by a person or relayed to practice staff based on the agreed call protocol.",
        second:
          "Escalated to staff when rules, evidence, permissions, or system availability do not support completion.",
      },
      {
        label: "Evidence",
        first:
          "Commonly a call record and message; reporting depth depends on the service.",
        second:
          "Separates attempts, handoffs, tool execution, and verified outcomes so unresolved work stays visible.",
      },
      {
        label: "Best fit",
        first:
          "Teams that prioritize human-only interaction, overflow coverage, or dependable message capture.",
        second:
          "Teams prepared to redesign repeatable workflows, integrate systems, govern automation, and measure outcomes.",
      },
    ],
  },
  questions: [
    {
      question:
        "Is an AI receptionist always better than a medical answering service?",
      answer:
        "No. A traditional service may be the better fit when human-only interaction or message capture is the actual requirement. Acuity fits teams that want specific operational workflows completed and are prepared to define the rules, integrations, handoffs, and evidence required to do that safely.",
    },
    {
      question: "Does an AI receptionist replace medical-practice staff?",
      answer:
        "That is not the operating goal. Acuity is designed to complete bounded, repeatable work and make unresolved needs visible. Staff retain responsibility for clinical judgment, ambiguous situations, consequential decisions, and the exceptions defined by the practice.",
    },
    {
      question: "Can an AI receptionist update our existing systems?",
      answer:
        "Potentially, when a supported integration and approved workflow exist. Acuity scopes each connection, permission, action, failure mode, and source of truth with the practice. Integration is tested at the real system boundary rather than assumed from a product category.",
    },
    {
      question: "How should we evaluate privacy and security claims?",
      answer:
        "Review the actual data flow, access controls, retention, subprocessors, agreements, integration architecture, and incident responsibilities with your legal and security teams. No conversational interface or vendor label establishes compliance by itself.",
    },
    {
      question: "What is the safest way to compare the two models?",
      answer:
        "Choose one high-value patient-access workflow. Baseline its current volume, failures, staff effort, and outcome rate. Then compare each option against the same rules, exception cases, handoffs, system updates, and evidence of completion before expanding the scope.",
    },
  ],
  related: [
    {
      href: "/method",
      label: "The Acuity Health Method",
      description:
        "See how Acuity baselines, redesigns, tests, and scales patient-access operations.",
    },
    {
      href: "/advancedmd-ai-receptionist",
      label: "AdvancedMD AI receptionist",
      description:
        "Evaluate an operating model designed around approved workflows in AdvancedMD.",
    },
    {
      href: "/ai-receptionist-for-ophthalmology",
      label: "AI receptionist for ophthalmology",
      description:
        "Explore how the model applies to the patient-access work of an ophthalmology practice.",
    },
  ],
}

export default function AiReceptionistComparisonPage() {
  return <CommercialPage spec={pageSpec} />
}
