import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { Lock, Loader2 } from 'lucide-react'
import { api, UnauthorizedError, type Settings } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { SectionShell, selectClass } from '../section-shell'

export function AuthSection({ settings }: { settings: Settings }) {
  const queryClient = useQueryClient()
  const a = settings.auth
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [pwError, setPwError] = useState<string | null>(null)

  const mode = useMutation({
    mutationFn: (r: string) => api.setAuthMode(r),
    onSuccess: (res) => {
      queryClient.setQueryData<Settings>(['settings'], (old) =>
        old ? { ...old, auth: { ...old.auth, required: res.required } } : old,
      )
      toast.success('Authentication mode updated')
    },
    onError: (e) =>
      toast.error('Could not update mode', {
        description: e instanceof Error ? e.message : String(e),
      }),
  })

  const changePw = useMutation({
    mutationFn: () => api.changePassword(current, next),
    onSuccess: () => {
      setCurrent('')
      setNext('')
      setConfirm('')
      setPwError(null)
      toast.success('Password changed')
    },
    onError: (e) =>
      setPwError(
        e instanceof UnauthorizedError
          ? 'Current password is incorrect.'
          : e instanceof Error
            ? e.message
            : String(e),
      ),
  })

  const submitPw = (e: React.FormEvent) => {
    e.preventDefault()
    setPwError(null)
    if (next !== confirm) {
      setPwError('Passwords do not match.')
      return
    }
    changePw.mutate()
  }

  return (
    <SectionShell
      icon={Lock}
      title="Authentication"
      description="Sign-in for the web UI. Machine clients use the API key below instead."
    >
      <div>
        <div className="mb-1 text-xs font-medium text-muted-foreground">Signed in as</div>
        <div className="text-sm font-medium">{a.username || '—'}</div>
      </div>

      <label className="block">
        <span className="mb-1 block text-xs font-medium text-muted-foreground">
          Require authentication
        </span>
        <select
          value={a.required}
          onChange={(e) => mode.mutate(e.target.value)}
          className={selectClass}
        >
          <option value="enabled">Always</option>
          <option value="local">Except on local addresses</option>
        </select>
        <span className="mt-1 block text-[11px] text-faint">
          “Except on local addresses” skips login for LAN/loopback clients (but never
          for reverse-proxied requests).
        </span>
      </label>

      <form onSubmit={submitPw} className="space-y-3 border-t pt-4">
        <div className="text-xs font-medium text-muted-foreground">Change password</div>
        <Input
          type="password"
          placeholder="Current password"
          autoComplete="current-password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
        <div className="grid gap-3 sm:grid-cols-2">
          <Input
            type="password"
            placeholder="New password"
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
          />
          <Input
            type="password"
            placeholder="Confirm new password"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </div>
        {pwError && <p className="text-xs text-destructive">{pwError}</p>}
        <Button type="submit" size="sm" disabled={changePw.isPending || !current || !next}>
          {changePw.isPending && <Loader2 className="size-4 animate-spin" />}
          Update password
        </Button>
      </form>
    </SectionShell>
  )
}
