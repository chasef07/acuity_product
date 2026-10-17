import { Button } from "@/components/ui/button"
import { useScorecardQuestions } from "@/lib/clients/call-review"
import { OptionSelect } from "./manage-agent-page"

export function CallFailureFilter({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const current = useScorecardQuestions()
  const previous = useScorecardQuestions("typesafe-scorecard-v7")
  const oldest = useScorecardQuestions("typesafe-scorecard-v6")
  const catalogs = [current, previous, oldest]
  const judges = new Map<string, { value: string; label: string }>()
  for (const catalog of catalogs) {
    if (catalog.status !== "ready") continue
    for (const question of catalog.data.questions) {
      if (question.source === "judge" && question.failure && !judges.has(question.key)) {
        judges.set(question.key, { value: question.key, label: question.label })
      }
    }
  }
  const failed = catalogs.filter((catalog) => catalog.status === "failed")
  const items = [
    { value: "any", label: "Any judge failure" },
    { value: "all", label: "All calls" },
    ...judges.values(),
  ]
  return <div className="flex flex-wrap items-center gap-2">
    <OptionSelect label="Judge flags" value={value} items={items} onChange={onChange} />
    {failed.length > 0 && <span className="text-xs text-destructive">Judge list unavailable. <Button size="xs" variant="outline" onClick={() => failed.forEach((catalog) => catalog.retry())}>Retry judge list</Button></span>}
  </div>
}
