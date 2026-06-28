"use client";

import { LoaderCircle } from "lucide-react";

import EditorPage from "@/pages/EditorPage";
import { useAuthGuard } from "@/lib/use-auth-guard";

export default function RetouchPage() {
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/image");

  if (isCheckingAuth || !session) {
    return (
      <div className="flex min-h-[40vh] items-center justify-center">
        <LoaderCircle className="size-5 animate-spin text-slate-400" />
      </div>
    );
  }

  return <EditorPage />;
}
