"use client"

import { useState } from "react"
import { CheckIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { OperatorAiCallTags } from "@/lib/api/generated/types.gen"
import { setCallTag, useCallTags } from "@/lib/clients/agent-calls"

export function CallManualTags({ interactionID, onChange }: {
  interactionID: string
  onChange: (id: string, tags: OperatorAiCallTags) => void
}) {
  const loaded = useCallTags(interactionID)
  const [saved, setSaved] = useState<OperatorAiCallTags>()
  const [name, setName] = useState("")
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState("")
  const tags = saved ?? (loaded.status === "ready" ? loaded.data : undefined)
  const error =
    saveError ||
    (loaded.status === "failed"
      ? loaded.failure.kind === "signedOut" || loaded.failure.kind === "unauthenticated"
        ? "Sign in again to load tags."
        : "Tags could not be loaded."
      : "")

  async function save(tag: string, applied: boolean) {
    if (saving) return
    setSaving(true)
    setSaveError("")
    const result = await setCallTag(interactionID, tag, applied)
    setSaving(false)
    if (result.ok) {
      setSaved(result.data)
      onChange(interactionID, result.data)
      setName("")
    } else {
      const { kind } = result.failure
      setSaveError(kind === "signedOut" || kind === "unauthenticated" ? "Sign in again to save tags." : "Tag could not be saved. Please try again.")
    }
  }

  return (
    <section aria-label="Manual tags" className="border-b px-5 py-4 sm:px-6">
      <h2 className="text-sm font-semibold">Manual tags</h2>
      <p className="mt-1 text-xs text-muted-foreground">Click a tag to add or remove it. Tags are shared within this practice.</p>
      {!tags && !error && <p className="mt-3 text-xs text-muted-foreground">Loading tags…</p>}
      {tags && <>
        <div className="mt-3 flex flex-wrap gap-1.5">
          {tags.available.map((tag) => {
            const applied = tags.selected.includes(tag)
            return <Button key={tag} type="button" variant={applied ? "secondary" : "outline"} size="xs" className="h-auto max-w-full whitespace-normal break-words text-left" aria-pressed={applied} disabled={saving} onClick={() => void save(tag, !applied)}>
              {applied && <CheckIcon aria-hidden="true" />}{tag}
            </Button>
          })}
          {tags.available.length === 0 && <p className="text-xs text-muted-foreground">No tags yet. Create the first one below.</p>}
        </div>
        <form className="mt-3 flex gap-2" onSubmit={(event) => { event.preventDefault(); if (name.trim()) void save(name.trim(), true) }}>
          <Input aria-label="New tag" placeholder="New tag" maxLength={60} value={name} disabled={saving} onChange={(event) => setName(event.target.value)} />
          <Button type="submit" variant="outline" size="sm" disabled={saving || !name.trim()}>Add tag</Button>
        </form>
      </>}
      {saving && <p role="status" className="mt-2 text-xs text-muted-foreground">Saving tag…</p>}
      {error && <div className="mt-2 text-xs text-destructive" role="alert">{error}{!tags && <Button variant="ghost" size="xs" onClick={loaded.retry}>Retry loading tags</Button>}</div>}
    </section>
  )
}
