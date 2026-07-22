import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { FolderTree } from 'lucide-react'
import { api, type Settings, type LibraryInput } from '@/lib/api'
import { Field, SectionShell, SectionFooter, selectClass, useSaveToast, type TestState } from '../section-shell'

const IMPORT_MODES = [
  { value: 'auto', label: 'Auto (hardlink, fall back to copy)' },
  { value: 'hardlink', label: 'Hardlink' },
  { value: 'copy', label: 'Copy' },
]

export function LibrarySection({ settings }: { settings: Settings }) {
  const l = settings.library
  const queryClient = useQueryClient()
  const [dir, setDir] = useState(l.dir)
  const [mode, setMode] = useState<NonNullable<LibraryInput['mode']>>(
    (l.mode as NonNullable<LibraryInput['mode']>) || 'auto',
  )
  const [testState, setTestState] = useState<TestState>(null)

  const body = (): LibraryInput => ({ dir, mode })

  const test = useMutation({
    mutationFn: () => api.testLibrary(body()),
    onSuccess: () => setTestState({ ok: true, message: 'Directory exists and is writable.' }),
    onError: (e) => setTestState({ ok: false, message: e instanceof Error ? e.message : String(e) }),
  })
  const save = useMutation({ mutationFn: () => api.updateLibrary(body()), ...useSaveToast(queryClient, 'Library') })

  return (
    <SectionShell
      icon={FolderTree}
      title="Library"
      description="Where completed downloads are imported. Empty disables import."
      configured={l.configured}
      footer={
        <SectionFooter
          onTest={() => test.mutate()}
          onSave={() => save.mutate()}
          testing={test.isPending}
          saving={save.isPending}
          testState={testState}
        />
      }
    >
      <Field
        label="Library directory"
        placeholder="/media/Anime"
        value={dir}
        onChange={(e) => setDir(e.target.value)}
        hint="Must be reachable from the Transpondarr host; share a mount with the download client for hardlinks."
      />
      <label className="block">
        <span className="mb-1 block text-xs font-medium text-muted-foreground">Import mode</span>
        <select
          value={mode}
          onChange={(e) => setMode(e.target.value as NonNullable<LibraryInput['mode']>)}
          className={selectClass}
        >
          {IMPORT_MODES.map((m) => (
            <option key={m.value} value={m.value}>
              {m.label}
            </option>
          ))}
        </select>
      </label>
    </SectionShell>
  )
}
