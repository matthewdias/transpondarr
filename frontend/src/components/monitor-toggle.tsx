import { Eye, EyeOff } from "lucide-react";
import { Toggle } from "@/components/ui/toggle";

// One control on all three surfaces, so the off state marks a row identically
// wherever it is shown.
export function MonitorToggle({
  monitored,
  itemNumber,
  onChange,
  disabled,
}: {
  monitored: boolean;
  // Absent for a film, which has one control and so nothing to disambiguate.
  itemNumber?: number;
  onChange: (monitored: boolean) => void;
  disabled?: boolean;
}) {
  const Icon = monitored ? Eye : EyeOff;
  const what = itemNumber === undefined ? "" : ` episode ${itemNumber}`;
  return (
    <Toggle
      variant="monitor"
      size="icon"
      pressed={monitored}
      disabled={disabled}
      onPressedChange={onChange}
      title={monitored ? "Stop monitoring" : "Monitor"}
      aria-label={monitored ? `Stop monitoring${what}` : `Monitor${what}`}
      className="shrink-0"
    >
      <Icon />
    </Toggle>
  );
}
