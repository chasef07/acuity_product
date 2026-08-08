type AuthEnvironment = Record<string, string | undefined>

export function googleProviderConfiguration(
  environment: AuthEnvironment = process.env
) {
  const clientId = environment.GOOGLE_CLIENT_ID?.trim()
  const clientSecret = environment.GOOGLE_CLIENT_SECRET?.trim()
  if (!clientId && !clientSecret) {
    return undefined
  }
  if (!clientId || !clientSecret) {
    throw new Error(
      "GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be configured together"
    )
  }
  return {
    google: {
      clientId,
      clientSecret,
      prompt: "select_account" as const,
    },
  }
}

export function googleAuthEnabled(
  environment: AuthEnvironment = process.env
): boolean {
  return googleProviderConfiguration(environment) !== undefined
}
