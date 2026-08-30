import {
  TrustPage,
  type TrustPageSpec,
} from "@/components/marketing/trust-page"
import { createPublicPageMetadata, siteConfig } from "@/lib/site"

const description =
  "Review Acuity Health's public security, privacy, and HIPAA posture, including safeguards, service boundaries, BAAs, and shared responsibilities."

export const metadata = createPublicPageMetadata({
  path: "/security",
  title: "Security, Privacy & HIPAA",
  description,
})

const pageSpec: TrustPageSpec = {
  route: "/security",
  eyebrow: "Public security posture",
  title: "Security, privacy, and HIPAA at Acuity Health.",
  description,
  introduction: `Acuity Health is operated by ${siteConfig.legalName}. We design our patient-access services to protect information through explicit access boundaries, validated workflows, operational evidence, and shared responsibility with each medical practice.`,
  updated: "August 30, 2026",
  updatedIso: "2026-08-30",
  notice: {
    title: "A public overview, not a compliance badge.",
    body:
      "This page is not a third-party certification, legal opinion, absolute security guarantee, SOC 2 report, or substitute for a signed Business Associate Agreement. Controls, agreements, integrations, and operating evidence must be reviewed for the exact service and deployment in scope.",
  },
  contact: {
    title: "Start a confidential security review.",
    body:
      "Security, privacy, and compliance teams can request the evidence appropriate to a proposed deployment. Supporting materials are shared confidentially and scoped to the services under review.",
    email: "chase@acuityhealth.io",
  },
  sections: [
    {
      id: "service-boundary",
      title: "A defined service boundary",
      paragraphs: [
        "Acuity Health supports administrative patient access and practice operations, including calls, scheduling workflows, staff handoffs, and evidence around completed or unresolved work. Customer-selected integrations vary by deployment.",
        "The service does not diagnose, independently provide clinical triage, prescribe treatment, or replace emergency services. Each medical practice approves its workflows, patient notices, recording and AI disclosures, escalation paths, and clinical boundaries.",
      ],
    },
    {
      id: "access-controls",
      title: "Scoped access",
      paragraphs: [
        "Access is designed around the customer security boundary. Authorized users are limited by Practice, permitted Locations, and assigned roles. Access outside those boundaries is not implied by a login or by possession of patient context.",
      ],
      bullets: [
        "Practice boundaries separate one customer's workspace from another.",
        "Location Scope limits Staff access to all or selected Locations as authorized by the Practice.",
        "Roles distinguish customer responsibilities from Acuity-wide operational access.",
        "Access Grants are email-bound and revocable. An Access Grant becomes a Membership only when a matching verified User signs in.",
      ],
    },
    {
      id: "application-safeguards",
      title: "Application safeguards",
      paragraphs: [
        "Our current control approach uses explicit technical checks at access, integration, workflow, and delivery boundaries. Operating effectiveness and deployment-specific configuration are validated separately.",
      ],
      bullets: [
        "Selected service connections verify requests or require configured credentials before accepting work.",
        "Credentials and secrets are supplied at runtime rather than committed to application code.",
        "Typed schemas, input validation, idempotency controls, explicit states, and bounded work protect workflow integrity.",
        "Selected operational events preserve actor, time, scope, action, and outcome context for support and accountability.",
        "Version control and automated formatting, linting, type, test, and build checks support controlled software delivery.",
      ],
    },
    {
      id: "information-handling",
      title: "Purpose-limited information handling",
      paragraphs: [
        "We seek to use patient information only for the contracted service and authorized workflow. Information made available to a model, integration, employee, or contractor should be limited to what that work requires.",
        "A phone number, name, transcript, or handoff can provide useful context, but it does not by itself establish verified patient identity or become the authoritative medical record. The customer's designated system remains authoritative for the records it owns.",
      ],
    },
    {
      id: "hipaa-and-baas",
      title: "HIPAA and Business Associate Agreements",
      paragraphs: [
        "When Acuity Health handles protected health information as a business associate, an appropriate Business Associate Agreement must govern that relationship. Our control standard also calls for appropriate agreements and technical review before a service provider handles protected health information for an Acuity deployment.",
        "Agreement coverage and technical configuration must be confirmed for the exact products, accounts, services, and customer workflow in use. An executed agreement alone does not prove that every technical or operational control is effective, and this page does not establish HIPAA compliance for a particular deployment.",
      ],
    },
    {
      id: "risk-and-incidents",
      title: "Risk and incident response",
      paragraphs: [
        "Security is an ongoing operating responsibility. Our process is designed to identify and assess risk, respond to credible security concerns, preserve relevant evidence, remediate confirmed issues, and improve controls after failures.",
      ],
      bullets: [
        "Detect and triage a report, alert, or unusual event.",
        "Contain suspected exposure while preserving evidence needed for assessment.",
        "Assess scope, impact, affected systems or information, and applicable obligations.",
        "Notify affected customers as required by the applicable Business Associate Agreement and law.",
        "Remediate the cause, document the response, and update controls or procedures.",
      ],
    },
    {
      id: "shared-responsibility",
      title: "Shared responsibility",
      paragraphs: [
        "A secure deployment depends on both Acuity Health and the medical practice. The customer controls its users, endpoints, networks, systems of record, workflow approvals, patient notices, and escalation policies. Acuity controls the service components and responsibilities assigned to it by the customer agreement.",
      ],
      bullets: [
        "Acuity restricts authorized service access, protects its managed credentials, enforces configured workflows, and responds to incidents within its scope.",
        "The medical practice authorizes users, removes access promptly, approves workflows and disclosures, and maintains its own endpoint and record-system controls.",
        "Both parties should report suspected misuse or compromise promptly and keep deployment decisions documented.",
      ],
    },
    {
      id: "assurance",
      title: "Evidence over assurances",
      paragraphs: [
        "Security claims should be tested against the actual deployment. We maintain supporting agreements, control descriptions, and technical or operational evidence separately for confidential review when appropriate.",
        "Public marketing language cannot replace deployment scoping, legal review, a signed Business Associate Agreement, or evidence that controls are operating as intended.",
      ],
    },
  ],
  related: [
    {
      href: "/privacy-policy",
      label: "Privacy Policy",
      description:
        "See how Acuity Health describes its collection, use, disclosure, and retention practices.",
    },
    {
      href: "/terms-of-service",
      label: "Terms of Service",
      description:
        "Review the public terms and the boundary between general use and signed customer agreements.",
    },
  ],
}

export default function SecurityPage() {
  return <TrustPage spec={pageSpec} />
}
