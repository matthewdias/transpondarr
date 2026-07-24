import { SidebarTrigger } from "@/components/ui/sidebar";

export function Topbar({
  title,
  breadcrumb,
  actions,
}: {
  title?: string;
  breadcrumb?: React.ReactNode;
  actions?: React.ReactNode;
}) {
  return (
    <header className="sticky top-0 z-10 flex items-center gap-3 border-b bg-background/85 px-4 py-3 backdrop-blur-md sm:px-6">
      <SidebarTrigger className="md:hidden" />
      {breadcrumb ?? (
        <h1 className="text-base font-semibold tracking-tight">{title}</h1>
      )}
      {actions && (
        <div className="ml-auto flex items-center gap-2">{actions}</div>
      )}
    </header>
  );
}
