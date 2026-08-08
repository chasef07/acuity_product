type AuthEnvironment = Record<string, string | undefined>

type PortalAuthenticationConfiguration = {
  socialProviders: NonNullable<ReturnType<typeof googleProviderConfiguration>>
}

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

export function portalAuthenticationConfiguration(
  environment: AuthEnvironment = process.env
): PortalAuthenticationConfiguration {
  const socialProviders = googleProviderConfiguration(environment)
  if (!socialProviders) {
    throw new Error("Google authentication is required")
  }
  return { socialProviders }
}
