import { useEffect } from "react";
import { useLocation } from "react-router-dom";

import { AnimatedRoutes } from "@/app/animated-routes";
import { TopNav } from "@/components/top-nav";

export function AppShell() {
  const location = useLocation();
  const isRetouchRoute = location.pathname.startsWith("/retouch");

  useEffect(() => {
    document.documentElement.classList.toggle("retouch-fullscreen", isRetouchRoute);
    document.body.classList.toggle("retouch-fullscreen", isRetouchRoute);

    return () => {
      document.documentElement.classList.remove("retouch-fullscreen");
      document.body.classList.remove("retouch-fullscreen");
    };
  }, [isRetouchRoute]);

  if (isRetouchRoute) {
    return (
      <main className="fixed inset-0 h-dvh w-dvw overflow-hidden bg-[#f5f7fa] text-foreground">
        <div className="flex h-full w-full flex-col gap-2 px-3 py-3 sm:px-5 lg:px-6">
          <TopNav />
          <div className="min-h-0 flex-1 overflow-hidden">
            <AnimatedRoutes />
          </div>
        </div>
      </main>
    );
  }

  return (
    <main className="min-h-screen bg-background text-foreground">
      <div className="mx-auto flex min-h-screen max-w-[1440px] flex-col gap-2 px-3 py-3 sm:px-5 lg:px-6">
        <TopNav />
        <AnimatedRoutes />
      </div>
    </main>
  );
}
