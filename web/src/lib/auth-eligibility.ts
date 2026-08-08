type EligibilityRequest = typeof fetch

type SignUpEligibility = {
  email: string
  portalAPIURL: string
  request?: EligibilityRequest
}

type UserEligibilityGate = {
  portalAPIURL: string
  request?: EligibilityRequest
}

export function createUserEligibilityGate({
  portalAPIURL,
  request = fetch,
}: UserEligibilityGate) {
  return async (user: { email: string }): Promise<boolean> =>
    isSignUpEligible({
      email: user.email,
      portalAPIURL,
      request,
    })
}

export async function isSignUpEligible({
  email,
  portalAPIURL,
  request = fetch,
}: SignUpEligibility): Promise<boolean> {
  const response = await request(
    `${portalAPIURL}/v1/access/sign-up-eligibility`,
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ email }),
      signal: AbortSignal.timeout(2_000),
    }
  ).catch(() => undefined)

  return response?.ok === true
}
