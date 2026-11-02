import type { KnowledgeSection } from "./api/generated/types.gen"

export function fold(text: string) {
  return text.normalize("NFD").replace(/\p{Diacritic}/gu, "").toLowerCase()
}

export function matches(entry: Pick<KnowledgeSection, "title" | "text">, words: string[]) {
  const haystack = fold(`${entry.title}\n${entry.text}`)
  return words.every((word) => haystack.includes(word))
}

export function publishedSections(entries: KnowledgeSection[]): KnowledgeSection[] {
  const used = new Set(entries.map((entry) => entry.id).filter(Boolean))
  return entries.map(({ id, title, text }) => {
    if (id) return { id, title, text }
    const slug = fold(title).replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 90) || "entry"
    let candidate = slug
    for (let suffix = 2; used.has(candidate); suffix++) candidate = `${slug}-${suffix}`
    used.add(candidate)
    return { id: candidate, title, text }
  })
}
