import { Button } from "@/components/ui/button"
import { useScorecardQuestions } from "@/lib/clients/call-review"
import { OptionSelect } from "./manage-agent-page"

export function CallFailureFilter({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const catalog = useScorecardQuestions()
  const items = [
    { value: "any", label: "Any judge failure" },
    { value: "all", label: "All calls" },
    ...(catalog.status === "ready" ? catalog.data.questions.filter((question) => question.source === "judge" && question.failure).map((question) => ({ value: question.key, label: question.label })) : []),
  ]
  return <div className="flex flex-wrap items-center gap-2">
    <OptionSelect label="Judge flags" value={value} items={items} onChange={onChange} />
    {catalog.status === "failed" && <span className="text-xs text-destructive">Judge list unavailable. <Button size="xs" variant="outline" onClick={catalog.retry}>Retry judge list</Button></span>}
  </div>
}
