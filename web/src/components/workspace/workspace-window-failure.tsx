export function WorkspaceWindowFailure({
  message,
  onRetry,
}: {
  message: string
  onRetry: () => void
}) {
  return (
    <p
      role="alert"
      className="flex items-center gap-2 px-2 py-1 text-xs text-destructive"
    >
      <span className="min-w-0 flex-1">{message}</span>
      <button
        type="button"
        className="shrink-0 font-medium underline underline-offset-2"
        onClick={onRetry}
      >
        Retry
      </button>
    </p>
  )
}
