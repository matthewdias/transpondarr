import { Construction } from 'lucide-react'
import { Topbar } from '@/components/topbar'

export function PlaceholderPage({ title, blurb }: { title: string; blurb: string }) {
  return (
    <>
      <Topbar title={title} />
      <div className="px-4 py-6 sm:px-6">
        <div className="mx-auto flex max-w-md flex-col items-center rounded-lg border border-dashed bg-card px-6 py-16 text-center">
          <Construction className="mb-4 size-8 text-faint" />
          <h2 className="text-base font-semibold">{title}</h2>
          <p className="mt-2 text-sm text-muted-foreground">{blurb}</p>
        </div>
      </div>
    </>
  )
}
