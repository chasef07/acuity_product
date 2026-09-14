import { readFile, writeFile } from "node:fs/promises"
import { createRequire } from "node:module"

const require = createRequire(import.meta.url)
const nextRequire = createRequire(require.resolve("next/package.json"))
const sharp = nextRequire("sharp")

// Keep every browser and installed-app icon in sync with the SVG source.
const app = new URL("../src/app/", import.meta.url)
const publicDir = new URL("../public/", import.meta.url)
const svg = await readFile(new URL("icon.svg", app), "utf8")
const artwork = svg.replace(/<svg[^>]*>/, "").replace("</svg>", "")
const backgroundSvg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <rect width="32" height="32" fill="#fff"/>
  <g transform="translate(3.2 3.2) scale(0.8)">${artwork}</g>
</svg>
`

async function render(source, size) {
  return sharp(Buffer.from(source), { density: size * 72 / 32 })
    .resize(size, size)
    .png()
    .toBuffer()
}

for (const size of [16, 32, 192, 512]) {
  const name = size < 100 ? `favicon-${size}x${size}.png` : `icon-${size}x${size}.png`
  await writeFile(new URL(name, publicDir), await render(svg, size))
}

const appleIcon = await render(backgroundSvg, 180)
await writeFile(new URL("apple-touch-icon.png", publicDir), appleIcon)
await writeFile(new URL("apple-icon.png", app), appleIcon)
await writeFile(new URL("acuity-icon-background.svg", publicDir), backgroundSvg)
await writeFile(new URL("icon-maskable-512x512.png", publicDir), await render(backgroundSvg, 512))

const sizes = [16, 32, 48]
const images = await Promise.all(sizes.map((size) => render(svg, size)))
const header = Buffer.alloc(6 + 16 * images.length)
header.writeUInt16LE(1, 2)
header.writeUInt16LE(images.length, 4)
let offset = header.length
for (const [index, png] of images.entries()) {
  const entry = 6 + 16 * index
  header[entry] = sizes[index]
  header[entry + 1] = sizes[index]
  header.writeUInt16LE(1, entry + 4)
  header.writeUInt16LE(32, entry + 6)
  header.writeUInt32LE(png.length, entry + 8)
  header.writeUInt32LE(offset, entry + 12)
  offset += png.length
}
await writeFile(new URL("favicon.ico", app), Buffer.concat([header, ...images]))
