import { readFileSync } from "node:fs"
import { expect, test } from "@playwright/test"
import ts from "typescript"

test("keypad feedback plays a short local tone in the browser", async ({ page }) => {
  // Exercise the production player with real browser audio decoding/playback.
  // Hardware routing and provider delivery require separate live acceptance.
  const source = readFileSync(
    new URL("../src/lib/calling/keypad-feedback.ts", import.meta.url),
    "utf8",
  )
  const script = ts.transpileModule(source, {
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.ES2022,
    },
  }).outputText
  await page.goto("/")
  await page.setContent('<button>Press 5</button><output></output>')
  await page.evaluate(async (script) => {
    const { createKeypadFeedback } = await import(
      `data:text/javascript;base64,${btoa(script)}`
    )
    const output = document.createElement("audio")
    const player = createKeypadFeedback()
    const createElement = document.createElement.bind(document)
    document.createElement = ((...args: Parameters<typeof createElement>) => {
      const element = createElement(...args)
      if (element instanceof HTMLAudioElement) {
        element.addEventListener("playing", () => {
          document.querySelector("output")!.textContent = JSON.stringify({
            duration: element.duration,
            sinkId: element.sinkId,
            localOnly: element.srcObject === null,
          })
        })
        element.addEventListener("ended", () => {
          document.querySelector("button")!.textContent = "Tone finished"
        })
      }
      return element
    }) as typeof document.createElement
    document.querySelector("button")!.onclick = () => void player.play("5", output)
  }, script)
  await page.getByRole("button", { name: "Press 5" }).click()
  await expect(page.getByRole("button", { name: "Tone finished" })).toBeVisible()
  const result = JSON.parse(await page.locator("output").innerText())
  expect(result.duration).toBeCloseTo(0.12, 3)
  expect(result.sinkId).toBe("")
  expect(result.localOnly).toBe(true)
})
