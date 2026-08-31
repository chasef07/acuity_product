import {
  TrustPage,
  type TrustPageSpec,
} from "@/components/marketing/trust-page"
import { createPublicPageMetadata, siteConfig } from "@/lib/site"

const description = `Read the terms governing use of the Acuity Health website and general services offered by ${siteConfig.legalName}.`

export const metadata = createPublicPageMetadata({
  path: "/terms-of-service",
  title: "Terms of Service",
  description,
})

const pageSpec: TrustPageSpec = {
  route: "/terms-of-service",
  eyebrow: "Terms of service",
  title: "The terms for using Acuity Health.",
  description,
  introduction: `These Terms of Service ("Terms") govern access to the Acuity Health website and general services offered by ${siteConfig.legalName}, doing business as Acuity Health ("Acuity Health," "we," "us," or "our").`,
  updated: "August 30, 2026",
  updatedIso: "2026-08-30",
  notice: {
    title: "Signed customer agreements come first.",
    body:
      "If your organization has a signed services agreement, order, or Business Associate Agreement with Acuity Health, that agreement governs the covered services and controls if it conflicts with these public Terms.",
  },
  contact: {
    title: "Questions about these Terms?",
    body: `Contact ${siteConfig.legalName} d/b/a Acuity Health about these Terms or the agreement that applies to your organization.`,
    email: "chase@acuityhealth.io",
  },
  sections: [
    {
      id: "acceptance-and-authority",
      title: "Acceptance and authority",
      paragraphs: [
        "By accessing or using our website or general services, you agree to these Terms. If you use them for an organization, you represent that you are authorized to act for that organization. If you do not agree, do not use the website or services.",
        "You are responsible for providing accurate information and for maintaining the confidentiality of credentials issued to you or your organization.",
      ],
    },
    {
      id: "service-boundary",
      title: "Administrative service boundary",
      paragraphs: [
        "Acuity Health supports administrative patient access, scheduling, communication, and practice operations. It does not provide medical advice, diagnose conditions, independently provide clinical triage, prescribe treatment, or replace a healthcare professional or emergency service.",
        "Customers are responsible for clinical decisions, emergency and escalation policies, patient notices, authorized users, and the accuracy of practice rules and information they provide. Anyone experiencing a medical emergency should contact emergency services rather than rely on this website or an Acuity Health service.",
      ],
    },
    {
      id: "acceptable-use",
      title: "Acceptable use",
      paragraphs: [
        "You may use the website and services only for lawful, authorized purposes and in accordance with these Terms and any applicable signed agreement.",
      ],
      bullets: [
        "Do not attempt to bypass access controls, probe for vulnerabilities, disrupt service, or access information outside your authorization.",
        "Do not submit unlawful, misleading, infringing, or malicious content or use the service to harm another person or system.",
        "Do not copy, reverse engineer, resell, or commercially exploit the website or services except as a signed agreement or applicable law permits.",
        "Notify us promptly if you suspect unauthorized access, credential misuse, or a security issue involving the service.",
      ],
    },
    {
      id: "customer-systems",
      title: "Customer systems and third-party services",
      paragraphs: [
        "Acuity Health may connect to customer-selected scheduling, communication, or record systems. Integrations and supported actions vary by deployment. The customer remains responsible for its systems, accounts, permissions, configurations, and records.",
        "Third-party products and links are governed by their own terms. We do not control their availability or independent practices. A successful conversation, transfer, or request does not by itself prove that an action was completed in a customer or third-party system.",
      ],
    },
    {
      id: "intellectual-property",
      title: "Intellectual property",
      paragraphs: [
        `The website, service software, trademarks, designs, and materials are owned by ${siteConfig.legalName} d/b/a Acuity Health or its licensors and are protected by applicable intellectual-property laws. These Terms grant only the limited right to use the website and general services as intended; they do not transfer ownership.`,
        "You retain rights in information you lawfully provide. You grant us the rights needed to process that information for the authorized service, subject to the applicable privacy, services, and Business Associate Agreements.",
      ],
    },
    {
      id: "privacy-and-security",
      title: "Privacy and security",
      paragraphs: [
        "Our Privacy Policy describes our general information practices. Signed customer agreements and Business Associate Agreements, when applicable, govern customer data and protected health information handled for covered services.",
        "You are responsible for using appropriate devices, networks, credentials, and access settings. No system can guarantee uninterrupted operation or absolute security.",
      ],
    },
    {
      id: "disclaimers",
      title: "Disclaimers",
      paragraphs: [
        "To the fullest extent permitted by law, the website and any general services not governed by a signed agreement are provided on an \"as is\" and \"as available\" basis. We disclaim warranties that are not expressly stated in a signed agreement, including implied warranties of merchantability, fitness for a particular purpose, and non-infringement.",
        "We do not guarantee that the website will always be available, error-free, or suitable for a specific clinical or operational purpose. Service commitments, if any, are stated in the applicable signed agreement.",
      ],
    },
    {
      id: "limitation-of-liability",
      title: "Limitation of liability",
      paragraphs: [
        `To the fullest extent permitted by law, ${siteConfig.legalName} and its affiliates will not be liable under these public Terms for indirect, incidental, special, consequential, exemplary, or punitive damages arising from use of the website or general services. Any liability terms for contracted services are governed by the applicable signed agreement.`,
        "Some limitations may not apply where prohibited by law. Nothing in these Terms excludes liability that cannot lawfully be excluded.",
      ],
    },
    {
      id: "changes",
      title: "Changes to these Terms",
      paragraphs: [
        "We may update these Terms by posting a revised version and changing the date above. Continued use after an update means the revised Terms apply to future website and general-service use. Changes to a signed customer agreement follow that agreement's amendment process.",
      ],
    },
  ],
  related: [
    {
      href: "/privacy-policy",
      label: "Privacy Policy",
      description:
        "See how Acuity Health collects, uses, protects, and shares information.",
    },
    {
      href: "/security",
      label: "Security, Privacy & HIPAA",
      description:
        "Review our public security posture and the boundaries around healthcare information.",
    },
  ],
}

export default function TermsOfServicePage() {
  return <TrustPage spec={pageSpec} />
}
