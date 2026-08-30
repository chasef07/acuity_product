import {
  CommercialPage,
  type CommercialPageSpec,
} from "@/components/marketing/commercial-page"
import { ophthalmologyDeploymentProof } from "@/lib/marketing-proof"
import { createPublicPageMetadata } from "@/lib/site"

const description =
  "Acuity Health deploys an AI receptionist for ophthalmology that completes approved scheduling calls, follows practice rules, and routes exceptions to staff."

export const metadata = createPublicPageMetadata({
  path: "/ai-receptionist-for-ophthalmology",
  title: "AI Receptionist for Ophthalmology Practices",
  description,
})

const ophthalmologyPage = {
  route: "/ai-receptionist-for-ophthalmology",
  schemaType: "Service",
  schemaName: "AI Receptionist for Ophthalmology",
  eyebrow: "AI receptionist for ophthalmology",
  title: "An AI receptionist built for ophthalmology patient access.",
  description,
  trace: {
    eyebrow: "The operating path",
    title: "From ophthalmology call to accountable outcome.",
    steps: [
      {
        label: "Patient need",
        title: "Understand what the caller needs.",
        description:
          "The agent gathers the patient need and the context required by the practice without treating caller-provided details as verified identity.",
      },
      {
        label: "Rules",
        title: "Apply the practice’s scheduling logic.",
        description:
          "Medical versus vision, visit type, location, provider, and availability rules narrow the safe next step.",
      },
      {
        label: "Action",
        title: "Complete the supported workflow.",
        description:
          "When the workflow is eligible and the connected system succeeds, the agent records the scheduling action in the EMR.",
      },
      {
        label: "Evidence + owner",
        title: "Record the outcome or hand off the exception.",
        description:
          "The practice can see what completed, what failed, and who owns clinical questions, ambiguity, unsupported patient needs, or system failures.",
      },
    ],
  },
  proof: ophthalmologyDeploymentProof,
  details: [
    {
      eyebrow: "Ophthalmology scheduling",
      title: "Carry practice rules into the call.",
      description:
        "An ophthalmology appointment is more than an open time. The workflow has to account for medical versus vision rules, visit type, patient status, location, provider, and the practice’s approved scheduling boundaries.",
      bullets: [
        "Practice-defined medical versus vision routing",
        "Location, provider, visit-type, and schedule constraints",
        "Required intake context before an appointment is offered",
        "An EMR action only after the connected workflow reports success",
      ],
    },
    {
      eyebrow: "Exceptions and handoffs",
      title: "Keep judgment with the right person.",
      description:
        "The agent does not improvise clinical advice or conceal uncertainty. When a patient need falls outside the approved workflow, it becomes a visible Task for the team that can resolve it.",
      bullets: [
        "Urgent symptoms and clinical questions follow the practice’s escalation policy",
        "Unsupported or ambiguous patient needs become visible staff Tasks",
        "Failed system actions remain unresolved and recoverable",
        "Staff receive the patient-need context and a clear next action",
      ],
    },
    {
      eyebrow: "Overflow and after hours",
      title: "Take on demand when the front desk cannot.",
      description:
        "Peak-volume and after-hours calls should not become tomorrow’s mystery queue. Approved routine work can continue, while unresolved needs remain visible for the team.",
      bullets: [
        "Overflow and after-hours demand handled through the approved workflow",
        "Supported booking, rescheduling, and cancellation paths",
        "Clear ownership when staff follow-up is required",
        "Outcome records that distinguish completed work from an attempted action",
      ],
    },
    {
      eyebrow: "Forward-deployed change",
      title: "Change the operation, not just the phone greeting.",
      description:
        "Acuity works with administrators and frontline teams to map rules, instrument failure, and improve the workflow after launch. The medical AI agent is one part of a patient-access operating model.",
      bullets: [
        "Baseline call failure and staff workload before rollout",
        "Translate local rules and exceptions into the deployment",
        "Review failures with practice operators and improve the workflow",
        "Scale only after outcomes are supported by evidence",
      ],
    },
  ],
  questions: [
    {
      question:
        "How is this different from an ophthalmology answering service?",
      answer:
        "An answering service typically answers and relays a message. Acuity redesigns the operating workflow so a medical AI agent can complete approved routine actions, record the outcome, and route exceptions as accountable staff work.",
    },
    {
      question: "Can the agent distinguish medical and vision needs?",
      answer:
        "Yes, when the practice’s approved rules provide a clear distinction. Acuity maps those rules into the workflow and sends ambiguous benefit or visit-type questions to staff rather than guessing.",
    },
    {
      question: "Can it schedule by location and provider?",
      answer:
        "The deployment can apply practice-defined location, provider, visit-type, and availability constraints. An appointment is represented as booked only after the connected scheduling workflow reports success.",
    },
    {
      question:
        "What happens when a caller has urgent symptoms or asks for clinical advice?",
      answer:
        "The agent does not provide medical advice. It follows the practice’s approved escalation policy and keeps the patient need visible for the appropriate clinical or administrative owner.",
    },
    {
      question: "What happens after hours?",
      answer:
        "Approved routine workflows can continue after hours. Patient needs that require staff judgment, fall outside the deployment scope, or fail in a connected system remain visible for follow-up instead of being presented as complete.",
    },
    {
      question: "Does this replace the front desk?",
      answer:
        "No. The goal is to return routine capacity to staff and give them cleaner, better-supported exceptions. Staff remain responsible for judgment, clinical questions, and work outside the approved automation boundary.",
    },
    {
      question: "Can this support optometry inside an eye-care group?",
      answer:
        "Acuity can map optometry and ophthalmology paths separately when both are in scope. Each service line needs explicit medical versus vision rules, provider constraints, and handoffs before launch.",
    },
    {
      question: "How do you measure whether the deployment works?",
      answer:
        "Acuity baselines the current operation, defines what counts as a completed outcome, and reviews failures after launch. The case-study results on this page separate recorded operating outcomes from estimated capacity and revenue impact.",
    },
  ],
  related: [
    {
      href: "/advancedmd-ai-receptionist",
      label: "AdvancedMD AI receptionist",
      description:
        "See how the operating workflow connects to AdvancedMD scheduling and staff work.",
    },
    {
      href: "/ai-receptionist-vs-medical-answering-service",
      label: "AI receptionist vs. answering service",
      description:
        "Compare completion, evidence, and exception handling before you choose an operating model.",
    },
    {
      href: "/method",
      label: "The Acuity Health Method",
      description:
        "See how workflow redesign and medical AI deployment move together.",
    },
  ],
} satisfies CommercialPageSpec

export default function OphthalmologyAIReceptionistPage() {
  return <CommercialPage spec={ophthalmologyPage} />
}
