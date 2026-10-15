<!-- BEGIN:nextjs-agent-rules -->
# This is NOT the Next.js you know

This version has breaking changes — APIs, conventions, and file structure may all differ from your training data. Read the relevant guide in `node_modules/next/dist/docs/` before writing any code. Heed deprecation notices.
<!-- END:nextjs-agent-rules -->

Render tests import base-ui before the harness creates its DOM, so base-ui portals (Sheet, Dialog, Popover) never mount there. Render the portal content directly, and cover opening it in Playwright.

Softphone runtime tests: `ManualClock.advance` drains only a few microtasks per timer, so a 5s backend-request timeout can fire before a deep async chain (acquire, refresh, media) settles. Use `clock.settle` when a step awaits several backend calls.

Softphone runtime retries that must run while nothing changed cannot live only in the full-state branch of `refreshOnce`: the calling-state etag covers the lease row, so an unchanged state returns 304 and skips that branch.
