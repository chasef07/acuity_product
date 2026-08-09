const expectedOfficeUsers = 30
const tokenBurstAllowancePerUser = 10

export function authRateLimitOptions(allowTestSession: boolean) {
  return {
    enabled: !allowTestSession,
    customRules: {
      "/token": {
        window: 10,
        max: expectedOfficeUsers * tokenBurstAllowancePerUser,
      },
    },
  } as const
}
