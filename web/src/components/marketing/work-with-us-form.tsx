"use client"

import { useRef, useState, type FormEvent } from "react"
import { ArrowRight } from "lucide-react"
import Link from "next/link"

import styles from "./enterprise-site.module.css"

type SubmissionState = "idle" | "submitting" | "success" | "error"

const formspreeEndpoint = "https://formspree.io/f/xgaevpbr"

const messages = {
  success: "Thanks. We’ll review the workflow and follow up shortly.",
  error: "We couldn’t send your request. Please try again shortly.",
} as const

export function WorkWithUsForm() {
  const [state, setState] = useState<SubmissionState>("idle")
  const formRef = useRef<HTMLFormElement>(null)

  async function submitForm(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()

    setState("submitting")

    try {
      const response = await fetch(formspreeEndpoint, {
        method: "POST",
        body: new FormData(event.currentTarget),
        headers: { Accept: "application/json" },
      })

      if (!response.ok) {
        setState("error")
        return
      }

      formRef.current?.reset()
      setState("success")
    } catch {
      setState("error")
    }
  }

  return (
    <form
      action={formspreeEndpoint}
      className={styles.inquiryForm}
      method="POST"
      onSubmit={submitForm}
      ref={formRef}
    >
      <label className={styles.formHoneypot} aria-hidden="true">
        Website
        <input autoComplete="off" name="_gotcha" tabIndex={-1} type="text" />
      </label>
      <input
        name="subject"
        type="hidden"
        value="New working-session request from {{ name }}"
      />
      <div className={styles.inquiryFieldGrid}>
        <label className={styles.formField}>
          <span>Your name</span>
          <input autoComplete="name" name="name" required type="text" />
        </label>
        <label className={styles.formField}>
          <span>Work email</span>
          <input autoComplete="email" name="email" required type="email" />
        </label>
        <label className={styles.formField}>
          <span>Role</span>
          <input autoComplete="organization-title" name="role" required type="text" />
        </label>
        <label className={styles.formField}>
          <span>Number of locations</span>
          <input inputMode="numeric" min="1" name="locationCount" required type="number" />
        </label>
        <label className={`${styles.formField} ${styles.formFieldWide}`}>
          <span>Practice</span>
          <input autoComplete="organization" name="practice" required type="text" />
        </label>
      </div>

      <div className={styles.formQuestions}>
        <label className={styles.formPrompt}>
          <strong>Which patient-access workflow should we focus on?</strong>
          <textarea
            name="workflow"
            placeholder="For example: new-patient scheduling or referral follow-up"
            required
            rows={2}
          />
        </label>

        <label className={styles.formPrompt}>
          <strong>Which KPIs or operational impact should we improve?</strong>
          <textarea
            name="baseline"
            placeholder="Share the impact or KPIs you want to improve"
            required
            rows={2}
          />
        </label>
      </div>

      <div className={styles.formFooter}>
        <button
          className={styles.darkButton}
          disabled={state === "submitting"}
          type="submit"
        >
          {state === "submitting" ? "Sending request…" : "Request a working session"}
          {state !== "submitting" && <ArrowRight aria-hidden="true" size={17} />}
        </button>
        {(state === "success" || state === "error") && (
          <p
            className={state === "success" ? styles.formSuccess : styles.formError}
            role={state === "error" ? "alert" : "status"}
          >
            {messages[state]}
          </p>
        )}
      </div>
      <p className={styles.formPrivacyNotice}>
        By submitting, you agree that Acuity Health may use this information to
        respond to your request. See our <Link href="/privacy-policy">Privacy Policy</Link>.
      </p>
    </form>
  )
}
