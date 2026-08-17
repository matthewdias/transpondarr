import { useId, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ListPlus, Loader2 } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { titlesQuery } from "@/lib/queries";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";

// The escape hatch for a title the provider published neither an episode count
// nor a schedule for. Deliberately never prefilled from a release search:
// maxItem is the bound decide uses to distrust a release's own numbering, so
// letting release names set it would make that guard inert.
export function SetEpisodeCountDialog({ titleId }: { titleId: number }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [count, setCount] = useState("");
  const inputId = useId();

  const parsed = Number(count);
  const valid = /^\d+$/.test(count) && parsed >= 1 && parsed <= 5000;

  const create = useMutation({
    mutationFn: () => api.setItemCount(titleId, parsed),
    onSuccess: (res) => {
      toast.success(`Created ${res.created} episodes`);
      queryClient.invalidateQueries({ queryKey: titlesQuery().queryKey });
      setOpen(false);
    },
    onError: (e) =>
      toast.error("Could not create the episodes", {
        description: e instanceof ApiError ? e.message : String(e),
      }),
  });

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setCount("");
      }}
    >
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          <ListPlus className="size-4" aria-hidden />
          Set episode count
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Set the episode count</DialogTitle>
          <DialogDescription>
            Creates episodes 1 to the number you give, so this title can be
            searched and downloaded. Nothing is stored: a count the provider
            publishes later still applies.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          <label htmlFor={inputId} className="text-sm font-medium">
            Episodes
          </label>
          <Input
            id={inputId}
            type="number"
            min={1}
            max={5000}
            value={count}
            onChange={(e) => setCount(e.target.value)}
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button
            onClick={() => create.mutate()}
            disabled={!valid || create.isPending}
          >
            {create.isPending && <Loader2 className="size-4 animate-spin" />}
            Create episodes
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
