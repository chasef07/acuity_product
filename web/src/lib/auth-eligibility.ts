type EligibilityRequest = typeof fetch

type SignUpEligibility = {
  email: string
  invitationToken?: string
  portalAPIURL: string
  request?: EligibilityRequest
}

type UserEligibilityGate = {
  portalAPIURL: string
  request?: EligibilityRequest
}

type UserCreationContext = {
  headers?: Headers
} | null

export function createUserEligibilityGate({
  portalAPIURL,
  request = fetch,
}: UserEligibilityGate) {
  return async (
    user: { email: string },
    context: UserCreationContext
  ): Promise<boolean> =>
    isSignUpEligible({
      email: user.email,
      invitationToken:
        context?.headers?.get("x-acuity-invitation-token") ?? undefined,
      portalAPIURL,
      request,
    })
}

export async function isSignUpEligible({
  email,
  invitationToken,
  portalAPIURL,
  request = fetch,
}: SignUpEligibility): Promise<boolean> {
  const response = await request(
    `${portalAPIURL}/v1/access/sign-up-eligibility`,
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        email,
        ...(invitationToken ? { invitationToken } : {}),
      }),
      signal: AbortSignal.timeout(2_000),
    }
  ).catch(() => undefined)

  return response?.ok === true
}
