import nodemailer from "nodemailer"

import { positiveInteger, required } from "@/lib/server-env"

export type AuthEmailKind = "verification" | "password-reset"

type AuthEmail = {
  kind: AuthEmailKind
  to: string
  url: string
}

interface AuthEmailSender {
  send(message: AuthEmail): Promise<void>
}

type TestEmailStore = Map<string, AuthEmail[]>

const globalEmailState = globalThis as typeof globalThis & {
  acuityTestEmails?: TestEmailStore
}

function testEmails(): TestEmailStore {
  globalEmailState.acuityTestEmails ??= new Map()
  return globalEmailState.acuityTestEmails
}

class TestEmailSender implements AuthEmailSender {
  async send(message: AuthEmail) {
    const key = message.to.trim().toLowerCase()
    const messages = testEmails().get(key) ?? []
    messages.push(message)
    testEmails().set(key, messages)
  }
}

class SMTPEmailSender implements AuthEmailSender {
  private readonly transport
  private readonly from: string

  constructor() {
    const host = required("SMTP_HOST")
    const port = positiveInteger("SMTP_PORT")
    const user = required("SMTP_USER")
    const password = required("SMTP_PASSWORD")
    this.from = required("AUTH_EMAIL_FROM")
    this.transport = nodemailer.createTransport({
      host,
      port,
      secure: port === 465,
      auth: { user, pass: password },
      connectionTimeout: 5_000,
      greetingTimeout: 5_000,
      socketTimeout: 10_000,
    })
  }

  async send(message: AuthEmail) {
    const subject =
      message.kind === "verification"
        ? "Verify your Acuity Portal email"
        : "Reset your Acuity Portal password"
    const action =
      message.kind === "verification"
        ? "Verify your email and continue"
        : "Reset your password"
    await this.transport.sendMail({
      from: this.from,
      to: message.to,
      subject,
      text: `${action}: ${message.url}`,
    })
  }
}

let sender: AuthEmailSender | undefined

export function getAuthEmailSender(): AuthEmailSender {
  if (sender) {
    return sender
  }
  const mode = process.env.AUTH_EMAIL_MODE
  if (mode === "test" && process.env.AUTH_ALLOW_TEST_EMAIL === "true") {
    sender = new TestEmailSender()
    return sender
  }
  if (mode === "smtp") {
    sender = new SMTPEmailSender()
    return sender
  }
  throw new Error("AUTH_EMAIL_MODE must be test or smtp")
}

export function latestTestEmail(
  email: string,
  kind: AuthEmailKind,
): AuthEmail | undefined {
  if (
    process.env.AUTH_EMAIL_MODE !== "test" ||
    process.env.AUTH_ALLOW_TEST_EMAIL !== "true"
  ) {
    return undefined
  }
  const messages = testEmails().get(email.trim().toLowerCase()) ?? []
  return messages.findLast((message) => message.kind === kind)
}
