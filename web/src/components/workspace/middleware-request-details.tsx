import type { MiddlewareRequestDiagnostic } from "@/lib/api/generated"

export function MiddlewareRequestDetails({
  requests,
  open,
}: {
  requests?: MiddlewareRequestDiagnostic[]
  open?: boolean
}) {
  if (!requests?.length) return null
  return (
    <details open={open || undefined} className="mt-2 text-xs">
      <summary className="cursor-pointer font-medium">
        Middleware requests ({requests.length})
      </summary>
      <ol className="mt-2 space-y-3">
        {requests.map((request) => (
          <li
            key={request.requestId}
            className="space-y-1 border-l-2 border-border pl-3"
          >
            <p className="font-mono">
              {request.operation} · attempt {request.attempt} ·{" "}
              {request.durationMs} ms
            </p>
            <p>
              {request.result}
              {request.httpStatus !== undefined &&
                ` · HTTP ${request.httpStatus}`}
              {request.responseStatus &&
                ` · response: ${request.responseStatus}`}
              {request.appointmentsStatus &&
                ` · appointments: ${request.appointmentsStatus}`}
              {request.outcome && ` · outcome: ${request.outcome}`}
              {request.category &&
                request.category !== "none" &&
                ` · ${request.category}`}
            </p>
            {request.failureReason && (
              <p>
                {request.failureReason}
                {request.failureDetail && ` · ${request.failureDetail}`}
                {request.retryable !== undefined &&
                  ` · retry ${request.retryable ? "allowed by agent policy" : "not allowed by agent policy"}`}
              </p>
            )}
            <p className="break-all font-mono text-muted-foreground">
              Request ID: {request.requestId}
            </p>
            {!!request.providerErrors?.length && (
              <ul className="space-y-1">
                {request.providerErrors.map((provider, index) => (
                  <li key={index}>
                    Provider: {provider.operation} · {provider.category}
                    {provider.httpStatus !== undefined &&
                      ` · HTTP ${provider.httpStatus}`}
                    {provider.code && ` · code ${provider.code}`}
                    {` · ${provider.durationMs} ms`}
                  </li>
                ))}
              </ul>
            )}
            {request.providerErrorCount !== undefined &&
              request.providerErrorCount >
                (request.providerErrors?.length ?? 0) && (
                <p className="text-muted-foreground">
                  Showing {request.providerErrors?.length ?? 0} of{" "}
                  {request.providerErrorCount} provider failures.
                </p>
              )}
          </li>
        ))}
      </ol>
    </details>
  )
}
