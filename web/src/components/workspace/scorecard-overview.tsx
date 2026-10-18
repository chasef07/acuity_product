"use client"

import { useState } from "react"
import { BookOpenTextIcon, ChevronRightIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import type {
  OperatorScorecardQuestion,
} from "@/lib/api/generated/types.gen"

function ScorecardDefinitions({ questions }: { questions: OperatorScorecardQuestion[] }) {
  return (
    <ul className="divide-y rounded-lg border bg-card">
      {questions.filter((question) => question.source === "judge").map((question) => (
        <li key={question.key}>
          <details className="group px-4 py-3">
            <summary className="flex cursor-pointer list-none items-start gap-2 text-sm [&::-webkit-details-marker]:hidden">
              <ChevronRightIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-90" aria-hidden="true" />
              <span>
                <span className="mr-1.5 font-mono text-xs text-muted-foreground">{question.code}</span>
                <span className="font-medium">{question.prompt}</span>
              </span>
            </summary>
            <div className="mt-2 space-y-2 pl-6 text-sm leading-6">
              <p className="text-muted-foreground">{question.question}</p>
              <dl className="grid gap-1 text-xs leading-5 sm:grid-cols-[2.5rem_1fr]">
                <dt className="font-medium">Yes</dt>
                <dd className="text-muted-foreground">{question.yes}</dd>
                <dt className="font-medium">No</dt>
                <dd className="text-muted-foreground">{question.no}</dd>
              </dl>
            </div>
          </details>
        </li>
      ))}
    </ul>
  )
}

export function ScorecardDefinitionsButton({ questions }: { questions: OperatorScorecardQuestion[] }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
        <BookOpenTextIcon aria-hidden="true" />
        Definitions
      </Button>
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent className="h-full overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:max-w-xl">
          <SheetHeader className="border-b px-5 py-4">
            <SheetTitle>Question definitions</SheetTitle>
            <SheetDescription>What the jury reads for each question, and what counts as yes or no.</SheetDescription>
          </SheetHeader>
          <div className="px-5 py-5">
            <ScorecardDefinitions questions={questions} />
          </div>
        </SheetContent>
      </Sheet>
    </>
  )
}
