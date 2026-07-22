import { Server } from 'lucide-react'
import { type Settings } from '@/lib/api'
import { SectionShell } from '../section-shell'

export function GeneralSection({ settings }: { settings: Settings }) {
  const g = settings.general
  const rows = [
    ['Version', g.version],
    ['Listen address', g.addr],
    ['Data directory', g.data_dir],
    ['Database', g.db_path],
  ]
  return (
    <SectionShell icon={Server} title="General" description="Read-only runtime information.">
      <dl className="divide-y">
        {rows.map(([k, v]) => (
          <div key={k} className="flex items-center justify-between gap-4 py-2 first:pt-0 last:pb-0">
            <dt className="text-xs text-muted-foreground">{k}</dt>
            <dd className="truncate font-mono text-xs">{v}</dd>
          </div>
        ))}
      </dl>
    </SectionShell>
  )
}
