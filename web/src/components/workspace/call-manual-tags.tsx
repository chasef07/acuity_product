"use client"

import { useEffect, useState } from "react"
import { CheckIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { portalClient } from "@/lib/api/client"
import { getOperatorAiCallTags, setOperatorAiCallTag } from "@/lib/api/generated/sdk.gen"
import type { OperatorAiCallTags } from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"

export function CallManualTags({ interactionID, onChange }: {
  interactionID: string
  onChange: (id: string, tags: OperatorAiCallTags) => void
}) {
  const [tags, setTags] = useState<OperatorAiCallTags>()
  const [name, setName] = useState("")
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  const [retry, setRetry] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    void (async () => {
      try {
        const token = await getAccessToken()
        if (!token) throw new Error("Sign in again to load tags.")
        const result = await getOperatorAiCallTags({ client: portalClient(token), path: { interactionId: interactionID }, signal: controller.signal })
        if (!result.data) throw new Error("Tags could not be loaded.")
        if (!controller.signal.aborted) { setTags(result.data); setError("") }
      } catch (error) {
        if (!controller.signal.aborted) setError(error instanceof Error ? error.message : "Tags could not be loaded.")
      }
    })()
    return () => controller.abort()
  }, [interactionID, retry])

  async function save(tag: string, applied: boolean) {
    if (saving) return
    setSaving(true)
    setError("")
    try {
      const token = await getAccessToken()
      if (!token) throw new Error("Sign in again to save tags.")
      const result = await setOperatorAiCallTag({ client: portalClient(token), path: { interactionId: interactionID }, body: { name: tag, applied } })
      if (!result.data) throw new Error("Tag could not be saved. Please try again.")
      setTags(result.data)
      onChange(interactionID, result.data)
      setName("")
    } catch (error) {
      setError(error instanceof Error ? error.message : "Tag could not be saved. Please try again.")
    } finally {
      setSaving(false)
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
      {error && <div className="mt-2 text-xs text-destructive" role="alert">{error}{!tags && <Button variant="ghost" size="xs" onClick={() => { setError(""); setRetry((value) => value + 1) }}>Retry loading tags</Button>}</div>}
    </section>
  )
}
