import { useId } from "react";
import { Link, useLocation } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CalendarDays,
  Compass,
  Library,
  ListChecks,
  Activity,
  Settings,
  Snail,
  Sun,
  Moon,
  Monitor,
  LogOut,
  ShieldCheck,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { api, AUTH_EXPIRED_EVENT } from "@/lib/api";
import { authStatusQuery, titlesQuery } from "@/lib/queries";
import { type Theme, useTheme } from "@/components/theme-provider";
import { Segmented, SegmentedItem } from "@/components/ui/segmented";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar";

const themes = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
] as const satisfies { value: Theme; label: string; icon: LucideIcon }[];

type NavItem = {
  title: string;
  to: string;
  icon: React.ComponentType<{ className?: string }>;
  badge?: number;
};

export function AppSidebar() {
  const location = useLocation();
  const { setOpenMobile } = useSidebar();
  const { theme, setTheme } = useTheme();
  const themeLabelId = useId();
  const queryClient = useQueryClient();
  const { data: titles } = useQuery(titlesQuery());
  const { data: auth } = useQuery(authStatusQuery());

  const logout = async () => {
    try {
      await api.logout();
    } catch {
      // ignore — we clear locally regardless
    }
    queryClient.clear();
    window.dispatchEvent(new Event(AUTH_EXPIRED_EVENT));
  };

  const isActive = (to: string) =>
    to === "/" ? location.pathname === "/" : location.pathname.startsWith(to);

  const library: NavItem[] = [
    { title: "Titles", to: "/", icon: Library, badge: titles?.length },
    { title: "Discovery", to: "/discovery", icon: Compass },
    { title: "Calendar", to: "/calendar", icon: CalendarDays },
    { title: "Wanted", to: "/wanted", icon: ListChecks },
    { title: "Activity", to: "/activity", icon: Activity },
  ];
  const system: NavItem[] = [
    { title: "Settings", to: "/settings", icon: Settings },
  ];

  const renderItem = (item: NavItem) => (
    <SidebarMenuItem key={item.to}>
      <SidebarMenuButton
        asChild
        isActive={isActive(item.to)}
        onClick={() => setOpenMobile(false)}
      >
        <Link to={item.to}>
          <item.icon className="size-4" />
          <span>{item.title}</span>
        </Link>
      </SidebarMenuButton>
      {item.badge != null && (
        <SidebarMenuBadge className="tabular-nums">
          {item.badge}
        </SidebarMenuBadge>
      )}
    </SidebarMenuItem>
  );

  return (
    <Sidebar collapsible="offcanvas">
      <SidebarHeader>
        <div className="flex items-center gap-2.5 px-2 py-2">
          <div className="grid size-7 place-items-center rounded-md bg-gradient-to-br from-primary to-primary/60 text-primary-foreground">
            <Snail className="size-4" />
          </div>
          <div className="text-base font-semibold tracking-tight">
            Transpondarr
          </div>
        </div>
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Library</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>{library.map(renderItem)}</SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        <SidebarGroup>
          <SidebarGroupLabel>System</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>{system.map(renderItem)}</SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>

      <SidebarFooter>
        <div className="flex items-center gap-2 px-3 py-2">
          <span id={themeLabelId} className="text-xs text-muted-foreground">
            Theme
          </span>
          <Segmented
            value={theme}
            onValueChange={(v) => setTheme(v as Theme)}
            aria-labelledby={themeLabelId}
          >
            {themes.map(({ value, label, icon: Icon }) => (
              <SegmentedItem
                key={value}
                value={value}
                title={label}
                aria-label={label}
              >
                <Icon />
              </SegmentedItem>
            ))}
          </Segmented>
        </div>
        {auth?.session ? (
          <button
            type="button"
            onClick={logout}
            className="flex items-center gap-2 rounded-md px-3 py-2 text-xs text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-foreground"
          >
            <LogOut className="size-4" /> Sign out
          </button>
        ) : auth?.required === "local" ? (
          // In `local` required-mode a loopback client is admitted with no session,
          // so there's nothing to sign out of — explain rather than show a dead button.
          <div className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
            <ShieldCheck className="size-4" /> Local access — no sign-in
            required
          </div>
        ) : null}
      </SidebarFooter>
    </Sidebar>
  );
}
