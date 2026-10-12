<!-- BEGIN:nextjs-agent-rules -->
# This is NOT the Next.js you know

This version has breaking changes — APIs, conventions, and file structure may all differ from your training data. Read the relevant guide in `node_modules/next/dist/docs/` before writing any code. Heed deprecation notices.
<!-- END:nextjs-agent-rules -->

Render tests import base-ui before the harness creates its DOM, so base-ui portals (Sheet, Dialog, Popover) never mount there. Render the portal content directly, and cover opening it in Playwright.
