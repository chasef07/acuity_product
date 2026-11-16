import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import { WorkspaceWindowFailure } from "./workspace-window-failure.tsx"

test("workspace window failure exposes one accessible retry control", () => {
  const failure = (
    <WorkspaceWindowFailure
      message="Texts are temporarily unavailable."
      onRetry={() => {}}
    />
  )
  const html = renderToStaticMarkup(failure)

  assert.match(html, /role="alert"/)
  assert.match(html, /Texts are temporarily unavailable\./)
  assert.match(html, /<button type="button"[^>]*>Retry<\/button>/)
})
