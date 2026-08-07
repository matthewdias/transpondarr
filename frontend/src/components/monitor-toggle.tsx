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
  itemNumber: number;
  onChange: (monitored: boolean) => void;
  disabled?: boolean;
}) {
  const Icon = monitored ? Eye : EyeOff;
  return (
    <Toggle
      variant="monitor"
      size="icon"
      pressed={monitored}
      disabled={disabled}
      onPressedChange={onChange}
      title={monitored ? "Stop monitoring" : "Monitor"}
      aria-label={
        monitored
          ? `Stop monitoring episode ${itemNumber}`
          : `Monitor episode ${itemNumber}`
      }
      className="shrink-0"
    >
      <Icon />
    </Toggle>
  );
}
