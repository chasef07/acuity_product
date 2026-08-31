import {
  TrustPage,
  type TrustPageSpec,
} from "@/components/marketing/trust-page"
import { createPublicPageMetadata, siteConfig } from "@/lib/site"

const description = `Read how ${siteConfig.legalName}, doing business as Acuity Health, collects, uses, protects, and shares information through its website and services.`

export const metadata = createPublicPageMetadata({
  path: "/privacy-policy",
  title: "Privacy Policy",
  description,
})

const pageSpec: TrustPageSpec = {
  route: "/privacy-policy",
  eyebrow: "Privacy policy",
  title: "How Acuity Health handles information.",
  description,
  introduction: `${siteConfig.legalName}, doing business as Acuity Health ("Acuity Health," "we," "us," or "our"), respects your privacy. This policy explains how we handle information through our website and services.`,
  updated: "August 30, 2026",
  updatedIso: "2026-08-30",
  notice: {
    title: "The right document depends on the relationship.",
    body:
      "This public policy describes our general practices. When Acuity Health handles protected health information for a medical practice, the applicable customer agreement and Business Associate Agreement govern that work. A practice's own privacy notice may also apply to patient information it controls.",
  },
  contact: {
    title: "Questions about privacy?",
    body: `Contact ${siteConfig.legalName} d/b/a Acuity Health about this policy or our general privacy practices. Patients seeking access to, correction of, or information about a medical record should contact the medical practice that controls that record.`,
    email: "chase@acuityhealth.io",
  },
  sections: [
    {
      id: "scope",
      title: "Scope",
      paragraphs: [
        "This policy applies to information collected through acuityhealth.io, business inquiries, and Acuity Health services. It does not replace the privacy notice of a medical practice, healthcare provider, or other customer that determines how patient information is used.",
        "Our services support administrative patient access and practice operations. They do not provide medical advice, diagnose conditions, independently provide clinical triage, or replace emergency services.",
      ],
    },
    {
      id: "information-we-collect",
      title: "Information we collect",
      paragraphs: [
        "The information we receive depends on how you interact with us and how a customer configures its service. We collect information you provide directly and limited technical or operational information generated when the website or service is used.",
      ],
      bullets: [
        "Business and website information, such as your name, organization, business email, phone number, and the details included in an inquiry.",
        "User identity and authorization information used to identify Users and control access to a Practice, Location, or role.",
        "Patient-access information needed for an authorized workflow, which may include contact details, call audio or transcripts when configured, appointment information, and practice-provided rules.",
        "Technical and operational information, such as device or browser data, IP address, session identifiers, service requests, integration outcomes, errors, and security events.",
      ],
    },
    {
      id: "how-we-use-information",
      title: "How we use information",
      paragraphs: [
        "We use information for the purposes described when it is collected, to perform our agreements, to operate and protect our business, and as otherwise permitted or required by law.",
      ],
      bullets: [
        "Respond to inquiries, evaluate potential engagements, and communicate about requested services.",
        "Provide, configure, support, and improve authorized patient-access and practice workflows.",
        "Authenticate users, limit access, protect service integrity, investigate failures, and respond to security concerns.",
        "Maintain operational evidence, meet contractual obligations, and comply with applicable legal requirements.",
      ],
    },
    {
      id: "hipaa-and-customer-data",
      title: "HIPAA and customer-directed services",
      paragraphs: [
        "When Acuity Health acts as a business associate under the Health Insurance Portability and Accountability Act (HIPAA), we use and disclose protected health information as permitted by the applicable Business Associate Agreement, customer instructions, and law.",
        "The customer remains responsible for its clinical decisions, patient notices, authorized users, workflows, disclosures, and system-of-record obligations. This public policy is not a Business Associate Agreement, certification, or legal opinion.",
      ],
    },
    {
      id: "how-we-share-information",
      title: "How we share information",
      paragraphs: [
        "We do not sell personal information. We may disclose information to the customer that directed the service, to contracted service providers supporting authorized operations, or when needed to protect rights, investigate misuse, complete a business transaction, or comply with law.",
        "Service providers receive information only for their assigned purpose and are subject to applicable contractual safeguards. The exact providers and integrations vary by deployment. We do not use customer protected health information for unrelated advertising or marketing.",
      ],
    },
    {
      id: "security-and-retention",
      title: "Security and retention",
      paragraphs: [
        "We use administrative, technical, and contractual safeguards designed for the information and services in scope. No internet transmission, software, or storage system can guarantee absolute security.",
        "We retain information according to customer agreements, service needs, security requirements, and applicable legal obligations, then delete or return it as required. Retention can vary by data type and deployment.",
      ],
    },
    {
      id: "choices-and-third-parties",
      title: "Your choices and third-party services",
      paragraphs: [
        "You may ask us to update or delete information submitted directly through our website, or opt out of marketing messages through the instructions in those messages. We evaluate requests based on our relationship with you and applicable obligations.",
        "Our website or services may link to or connect with third-party services. Their own privacy terms apply to information they control. Patients should direct medical-record or patient-rights requests to the medical practice responsible for the record.",
      ],
    },
    {
      id: "changes",
      title: "Changes to this policy",
      paragraphs: [
        "We may update this policy as our services or practices change. We will post the revised policy here and update the date above. Material contractual requirements remain governed by the applicable signed agreement.",
      ],
    },
  ],
  related: [
    {
      href: "/security",
      label: "Security, Privacy & HIPAA",
      description:
        "Review the public safeguards, operating boundaries, and shared responsibilities behind our services.",
    },
    {
      href: "/terms-of-service",
      label: "Terms of Service",
      description:
        "Read the terms that apply to the Acuity Health website and general service use.",
    },
  ],
}

export default function PrivacyPolicyPage() {
  return <TrustPage spec={pageSpec} />
}
