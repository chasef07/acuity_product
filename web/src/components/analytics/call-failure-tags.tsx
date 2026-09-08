"use client"

import { useState } from "react"
import { XIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { CallReviewTags, TagChange } from "@/lib/call-review-tags"

export function CallFailureTags({
  callID,
  state,
  onChange,
}: {
  callID: string
  state: CallReviewTags
  onChange: (change: TagChange) => boolean
}) {
  const [draft, setDraft] = useState("")
  const [saved, setSaved] = useState("")
  const assigned = state.calls[callID] ?? []
  const existing = state.tags.find(
    (tag) =>
      tag.toLowerCase() === draft.trim().replace(/\s+/g, " ").toLowerCase(),
  )
  function apply(tag: string, remove = false) {
    if (onChange({ callID, tag, remove })) {
      setDraft("")
      setSaved(
        remove
          ? "Tag removed. Saved in this browser."
          : "Tag applied. Saved in this browser.",
      )
    } else setSaved("")
  }
  return (
    <section aria-label="Failure tags" className="space-y-3">
      <div>
        <h2 className="text-sm font-semibold">Failure tags</h2>
        <p className="mt-1 text-xs text-muted-foreground">
          Create a reason or reuse one of your saved tags.
        </p>
      </div>
      {assigned.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {assigned.map((tag) => (
            <Button
              key={tag}
              size="sm"
              variant="secondary"
              aria-label={`Remove tag ${tag}`}
              onClick={() => apply(tag, true)}
            >
              {tag}
              <XIcon className="size-3" />
            </Button>
          ))}
        </div>
      )}
      <form
        className="flex gap-2"
        onSubmit={(event) => {
          event.preventDefault()
          if (draft.trim()) apply(draft)
        }}
      >
        <Input
          aria-label="New or existing failure tag"
          placeholder="e.g. get_availability"
          maxLength={80}
          value={draft}
          onChange={(event) => {
            setDraft(event.target.value)
            setSaved("")
          }}
        />
        <Button
          type="submit"
          size="sm"
          disabled={
            !draft.trim() || Boolean(existing && assigned.includes(existing))
          }
        >
          {existing ? "Apply tag" : "Create & tag"}
        </Button>
      </form>
      {state.tags.some((tag) => !assigned.includes(tag)) && (
        <div className="flex flex-wrap gap-2" aria-label="Saved failure tags">
          {state.tags
            .filter(
              (tag) =>
                !assigned.includes(tag) &&
                tag.toLowerCase().includes(draft.trim().toLowerCase()),
            )
            .map((tag) => (
              <Button
                key={tag}
                size="sm"
                variant="outline"
                aria-label={`Apply tag ${tag}`}
                onClick={() => apply(tag)}
              >
                {tag}
              </Button>
            ))}
        </div>
      )}
      <p role="status" className="text-xs text-muted-foreground">
        {saved || "Tags save locally and are available on every call."}
      </p>
    </section>
  )
}
