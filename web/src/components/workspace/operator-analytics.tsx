import { ChartNoAxesCombinedIcon } from "lucide-react"

import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"

export function OperatorAnalytics() {
  return (
    <section
      aria-labelledby="operator-analytics-title"
      className="flex min-h-0 flex-1 p-6"
    >
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <ChartNoAxesCombinedIcon />
          </EmptyMedia>
          <EmptyTitle id="operator-analytics-title">Analytics</EmptyTitle>
          <EmptyDescription>
            Transcripts, latency, and call performance will live here.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    </section>
  )
}
