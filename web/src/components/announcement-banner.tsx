"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Bell, Megaphone } from "lucide-react";

import { AnnouncementMarkdown } from "@/components/announcement-markdown";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { fetchVisibleAnnouncements, type Announcement, type AnnouncementTarget } from "@/lib/api";
import { cn } from "@/lib/utils";

function readKey(target: AnnouncementTarget) {
  return `read-announcements-${target}`;
}

function loadRead(target: AnnouncementTarget): Set<string> {
  try {
    const raw = localStorage.getItem(readKey(target));
    if (!raw) return new Set();
    return new Set(JSON.parse(raw) as string[]);
  } catch {
    return new Set();
  }
}

function saveRead(target: AnnouncementTarget, ids: Set<string>) {
  try {
    localStorage.setItem(readKey(target), JSON.stringify([...ids]));
  } catch {
    // localStorage full or unavailable
  }
}

export function AnnouncementNotifications({
  target,
  className,
}: {
  target: AnnouncementTarget;
  className?: string;
}) {
  const [announcements, setAnnouncements] = useState<Announcement[]>([]);
  const [read, setRead] = useState<Set<string>>(() => loadRead(target));
  const prevIdsRef = useRef<string>("");

  useEffect(() => {
    let active = true;

    const loadAnnouncements = async () => {
      try {
        const data = await fetchVisibleAnnouncements(target);
        if (active) {
          setAnnouncements(data.items);
        }
      } catch {
        if (active) {
          setAnnouncements([]);
        }
      }
    };

    void loadAnnouncements();
    return () => {
      active = false;
    };
  }, [target]);

  const unreadCount = useMemo(
    () => announcements.filter((a) => !read.has(a.id)).length,
    [announcements, read],
  );

  const handleOpenChange = useCallback(
    (open: boolean) => {
      if (open && announcements.length > 0) {
        const ids = announcements.map((a) => a.id).sort().join(",");
        if (ids === prevIdsRef.current) return;
        prevIdsRef.current = ids;

        setRead((prev) => {
          const next = new Set(prev);
          for (const a of announcements) next.add(a.id);
          saveRead(target, next);
          return next;
        });
      }
    },
    [announcements, target],
  );

  if (announcements.length === 0) {
    return null;
  }

  return (
    <Popover onOpenChange={handleOpenChange}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          className={cn(
            "relative size-10 rounded-full border-amber-200 bg-amber-50/95 p-0 text-amber-800 shadow-sm hover:bg-amber-100 hover:text-amber-900",
            className,
          )}
          aria-label={unreadCount > 0 ? `查看 ${unreadCount} 条未读公告` : "查看公告"}
        >
          <Bell className="size-4" />
          {unreadCount > 0 && (
            <span className="absolute -right-1 -top-1 flex min-w-5 items-center justify-center rounded-full bg-amber-600 px-1.5 text-[10px] font-semibold leading-5 text-white">
              {unreadCount}
            </span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[min(calc(100vw-2rem),400px)] p-0">
        <div className="border-b border-stone-100 px-4 py-3">
          <div className="flex items-center gap-2 text-sm font-semibold text-stone-900">
            <Bell className="size-4 text-amber-700" />
            通知提醒
          </div>
        </div>
        <div aria-live="polite" className="max-h-[min(64vh,520px)] overflow-y-auto p-3">
          <div className="flex flex-col gap-3">
            {announcements.map((announcement) => (
              <aside
                key={announcement.id}
                className="flex gap-3 rounded-2xl border border-amber-200/80 bg-amber-50/90 px-4 py-3 text-left"
              >
                <div className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-xl bg-amber-100 text-amber-800">
                  <Megaphone className="size-4" />
                </div>
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-semibold text-stone-900">{announcement.title.trim() || "公告"}</p>
                  <AnnouncementMarkdown className="mt-1 text-stone-700">{announcement.content}</AnnouncementMarkdown>
                </div>
              </aside>
            ))}
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}
