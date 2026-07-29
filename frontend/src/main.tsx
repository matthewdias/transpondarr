import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { HashRouter, Routes, Route } from "react-router";
import "./index.css";

import { ThemeProvider } from "@/components/theme-provider";
import { AuthGate } from "@/components/auth-gate";
import { Toaster } from "@/components/ui/sonner";
import { ApiError } from "@/lib/api";
import { AppLayout } from "@/components/app-layout";
import { SeriesListPage } from "@/pages/series-list";
import { DiscoveryPage } from "@/pages/discovery";
import { CalendarPage } from "@/pages/calendar";
import { SeriesDetailPage } from "@/pages/series-detail";
import { SettingsPage } from "@/pages/settings";
import { PlaceholderPage } from "@/pages/placeholder";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      refetchOnWindowFocus: false,
      // Don't hammer the server on client errors (401/404/422); retry once on 5xx.
      retry: (count, error) =>
        error instanceof ApiError && error.status < 500 ? false : count < 1,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <AuthGate>
          <HashRouter>
            <Routes>
              <Route element={<AppLayout />}>
                <Route index element={<SeriesListPage />} />
                <Route path="series/:id" element={<SeriesDetailPage />} />
                <Route path="discovery" element={<DiscoveryPage />} />
                <Route path="calendar" element={<CalendarPage />} />
                <Route
                  path="wanted"
                  element={
                    <PlaceholderPage
                      title="Wanted"
                      blurb="A cross-series view of every wanted item is coming. For now, open a series to search and grab its episodes."
                    />
                  }
                />
                <Route
                  path="activity"
                  element={
                    <PlaceholderPage
                      title="Activity"
                      blurb="A global download & import feed is coming. Per-series history lives on each series' History tab."
                    />
                  }
                />
                <Route path="settings" element={<SettingsPage />} />
              </Route>
            </Routes>
          </HashRouter>
        </AuthGate>
        <Toaster position="bottom-right" />
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
