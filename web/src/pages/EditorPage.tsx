"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { AlertTriangle, Check, Download, GitBranch, History, ImagePlus, Loader2, RotateCcw, Send, Trash2, Upload, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import {
  RetouchCanvas,
  type RetouchCanvasHandle,
} from "@/app/retouch/components/retouch-canvas";
import { buildRetouchPrompt, type RetouchMarkerColor } from "@/app/retouch/retouch-prompt";
import {
  cancelCreationTask,
  createImageEditTask,
  fetchCreationTasks,
  type CreationTask,
} from "@/lib/api";
import {
  useImageTreeStore,
  type ImageNode,
  type ImageTreeAsset,
} from "@/store/useImageTreeStore";
import {
  clearRetouchHistorySessions,
  deleteRetouchHistorySession,
  getRetouchHistoryStats,
  getRetouchSessionPreviewUrl,
  listRetouchHistorySessions,
  RETOUCH_HISTORY_CHANGED_EVENT,
  saveRetouchHistorySession,
  type RetouchHistorySession,
} from "@/store/retouch-history";
import type { StoredAuthSession } from "@/store/auth";

type PageStatus = "empty" | "editing" | "submitting" | "polling" | "success_split" | "error";
type SplitSelection = "source" | "result" | null;
type RetouchRequestMode = "api" | "mock";

const CREATION_TASK_POLL_INTERVAL_MS = 2000;
const RETOUCH_REQUEST_MODE_STORAGE_KEY = "chatgpt2api:retouch_request_mode";

function isRetouchRequestMode(value: unknown): value is RetouchRequestMode {
  return value === "api" || value === "mock";
}

function getStoredRetouchRequestMode(): RetouchRequestMode {
  if (typeof window === "undefined") {
    return "api";
  }

  try {
    const stored = window.localStorage.getItem(RETOUCH_REQUEST_MODE_STORAGE_KEY);
    return isRetouchRequestMode(stored) ? stored : "api";
  } catch {
    return "api";
  }
}

function createRandomId(prefix: string) {
  const random =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `${prefix}-${random}`;
}

function createImageAsset(file: File, dataUrl: string): ImageTreeAsset {
  return {
    id: createRandomId("upload"),
    url: dataUrl,
    name: file.name,
    source: "upload",
  };
}

function readFileAsDataUrl(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(new Error("读取上传图片失败"));
    reader.readAsDataURL(file);
  });
}

function createSessionTitle(fileName: string, fallbackPrompt: string) {
  const cleanFileName = fileName.replace(/\.[^.]+$/, "").trim();
  if (cleanFileName) {
    return cleanFileName.slice(0, 48);
  }
  const cleanPrompt = fallbackPrompt.trim();
  if (cleanPrompt) {
    return cleanPrompt.slice(0, 48);
  }
  return "未命名修图";
}

function createMockImageEditTask(clientTaskId: string, sourceImage: ImageTreeAsset, prompt: string): CreationTask {
  const now = new Date().toISOString();
  return {
    id: clientTaskId,
    status: "success",
    mode: "edit",
    created_at: now,
    updated_at: now,
    data: [
      {
        url: sourceImage.url,
        revised_prompt: `Mock result: ${prompt}`,
      },
    ],
    visibility: "private",
  };
}

function createGeneratedAsset(task: CreationTask): ImageTreeAsset {
  const item = task.data?.[0];
  const url = item?.url || (item?.b64_json ? `data:image/${item.output_format || task.output_format || "png"};base64,${item.b64_json}` : "");
  if (!url) {
    throw new Error("生成任务未返回图片数据");
  }

  return {
    id: createRandomId(`generated-${task.id}`),
    url,
    name: item?.revised_prompt || "生成结果",
    source: "generated",
    width: item?.width,
    height: item?.height,
  };
}

function sleep(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function formatCreationTaskErrorMessage(message: string) {
  const trimmed = String(message || "").trim();
  if (!trimmed) {
    return "生成图片失败";
  }

  const normalized = trimmed.toLowerCase();
  if (normalized.includes("user balance insufficient")) {
    return "用户余额不足";
  }
  if (normalized.includes("user quota exceeded")) {
    return "用户配额不足";
  }
  if (normalized.includes("no available image quota")) {
    return "当前没有可用的图片额度，请检查账号额度或稍后重试。";
  }
  if (normalized.includes("timed out waiting for async image generation")) {
    return "图片生成等待超时，建议稍后重试。";
  }
  if (normalized.includes("no images generated") && normalized.includes("model may have refused")) {
    return "没有生成图片，模型可能检测到敏感内容并拒绝了这次请求，请调整提示词后重试。";
  }

  return trimmed;
}

function dataUrlToFile(dataUrl: string, fileName: string) {
  const [header, payload = ""] = dataUrl.split(",");
  const mimeType = header.match(/^data:([^;]+)/)?.[1] || "image/png";
  const binary = atob(payload);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return new File([bytes], fileName, { type: mimeType });
}

async function imageAssetToFile(image: ImageTreeAsset, fallbackFile: File | null) {
  if (fallbackFile) {
    return fallbackFile;
  }
  const extension = getDownloadExtension(image.url);
  const fileName = sanitizeDownloadName(image.name || image.id) || `retouch-source.${extension}`;
  if (image.url.startsWith("data:")) {
    return dataUrlToFile(image.url, fileName);
  }

  const response = await fetch(image.url, { cache: "no-store" });
  if (!response.ok) {
    throw new Error("读取待编辑图片失败");
  }
  const blob = await response.blob();
  return new File([blob], fileName, { type: blob.type || `image/${extension}` });
}
function getNodePreviewImage(node: ImageNode) {
  return node.generatedImage ?? node.baseImage;
}

function formatElapsedTime(totalSeconds: number) {
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

function getImageLabel(image: ImageTreeAsset | undefined) {
  return image?.sequenceNumber ? `#${image.sequenceNumber}` : "#-";
}

function getDownloadExtension(url: string) {
  const dataMime = url.match(/^data:image\/([^;,]+)/)?.[1];
  if (dataMime) {
    return dataMime === "svg+xml" ? "svg" : dataMime;
  }

  const cleanUrl = url.split("?")[0]?.split("#")[0] ?? "";
  const extension = cleanUrl.match(/\.([a-z0-9]+)$/i)?.[1];
  return extension || "png";
}

function sanitizeDownloadName(value: string) {
  return value
    .trim()
    .replace(/[\\/:*?"<>|]+/g, "-")
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
}

function getImageDownloadFilename(image: ImageTreeAsset) {
  const label = getImageLabel(image).replace("#", "image-");
  const baseName = sanitizeDownloadName(image.name ?? label) || label;
  const extension = getDownloadExtension(image.url);

  if (new RegExp(`\\.${extension}$`, "i").test(baseName)) {
    return baseName;
  }

  return `${label}-${baseName}.${extension}`;
}

function downloadImageAsset(image: ImageTreeAsset) {
  const link = document.createElement("a");
  link.href = image.url;
  link.download = getImageDownloadFilename(image);
  link.rel = "noopener";
  document.body.appendChild(link);
  link.click();
  link.remove();
}

function getUniqueImages(nodesById: Record<string, ImageNode>) {
  const images = new Map<string, ImageTreeAsset>();
  for (const node of Object.values(nodesById)) {
    images.set(node.baseImage.id, node.baseImage);
    if (node.generatedImage) {
      images.set(node.generatedImage.id, node.generatedImage);
    }
  }
  return Array.from(images.values()).sort((a, b) => (a.sequenceNumber ?? 0) - (b.sequenceNumber ?? 0));
}

function getNodePreviewUrl(node: ImageNode | null) {
  if (!node) {
    return undefined;
  }
  return (node.generatedImage ?? node.baseImage).url;
}

function formatHistoryTime(value: string) {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) {
    return "刚刚";
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function buildRetouchSession(
  id: string,
  title: string,
  createdAt: string,
  draftPrompt: string,
  existing?: RetouchHistorySession,
): RetouchHistorySession | null {
  const tree = useImageTreeStore.getState().exportTree();
  if (!tree.rootNodeId || !tree.nodesById[tree.rootNodeId]) {
    return null;
  }
  const currentNode = tree.currentNodeId ? tree.nodesById[tree.currentNodeId] : tree.nodesById[tree.rootNodeId];
  return {
    ...tree,
    id,
    title: title.trim() || existing?.title || "未命名修图",
    createdAt: existing?.createdAt || createdAt,
    updatedAt: new Date().toISOString(),
    thumbnailUrl: getNodePreviewUrl(currentNode) || existing?.thumbnailUrl,
    draftPrompt,
  };
}
type EditorPageProps = {
  session: StoredAuthSession;
};

export default function EditorPage({ session }: EditorPageProps) {
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const retouchCanvasRef = useRef<RetouchCanvasHandle | null>(null);
  const generationRunIdRef = useRef(0);
  const activeTaskIdRef = useRef<string | null>(null);
  const sessionsRef = useRef<RetouchHistorySession[]>([]);
  const [prompt, setPrompt] = useState("");
  const [pageStatus, setPageStatus] = useState<PageStatus>("empty");
  const [splitSelection, setSplitSelection] = useState<SplitSelection>(null);
  const [isDragging, setIsDragging] = useState(false);
  const [generatingStartedAt, setGeneratingStartedAt] = useState<number | null>(null);
  const [generationElapsedSeconds, setGenerationElapsedSeconds] = useState(0);
  const [hasCanvasMarks, setHasCanvasMarks] = useState(false);
  const [markerColor, setMarkerColor] = useState<RetouchMarkerColor | null>(null);
  const [pendingSourceImage, setPendingSourceImage] = useState<ImageTreeAsset | null>(null);
  const [, setPendingMaskData] = useState<string | undefined>();
  const [errorMessage, setErrorMessage] = useState("");
  const [brushSize, setBrushSize] = useState(5);
  const [requestMode, setRequestMode] = useState<RetouchRequestMode>(getStoredRetouchRequestMode);
  const [sourceFilesByAssetId, setSourceFilesByAssetId] = useState<Record<string, File>>({});
  const [historySessions, setHistorySessions] = useState<RetouchHistorySession[]>([]);
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [isHistoryOpen, setIsHistoryOpen] = useState(false);
  const [isLoadingHistory, setIsLoadingHistory] = useState(true);
  const [deleteConfirm, setDeleteConfirm] = useState<{ type: "one"; id: string } | { type: "all" } | null>(null);
  const addRootNode = useImageTreeStore((state) => state.addRootNode);
  const addNode = useImageTreeStore((state) => state.addNode);
  const navigateNode = useImageTreeStore((state) => state.navigateNode);
  const getAncestors = useImageTreeStore((state) => state.getAncestors);
  const resetTree = useImageTreeStore((state) => state.resetTree);
  const replaceTree = useImageTreeStore((state) => state.replaceTree);
  const currentNodeId = useImageTreeStore((state) => state.currentNodeId);
  const rootNodeId = useImageTreeStore((state) => state.rootNodeId);
  const nodesById = useImageTreeStore((state) => state.nodesById);
  const currentNode = useImageTreeStore((state) =>
    state.currentNodeId ? state.nodesById[state.currentNodeId] : null,
  );
  const ancestors = useMemo(
    () => (currentNodeId ? getAncestors(currentNodeId) : []),
    [currentNodeId, getAncestors],
  );
  const imageList = useMemo(() => getUniqueImages(nodesById), [nodesById]);
  const rootNode = rootNodeId ? nodesById[rootNodeId] ?? null : null;
  const isGenerating = pageStatus === "submitting" || pageStatus === "polling";
  const isError = pageStatus === "error";
  const canUseMockRequestMode = session.role === "admin";
  const effectiveRequestMode: RetouchRequestMode = canUseMockRequestMode ? requestMode : "api";
  const sourceImage = currentNode?.baseImage;
  const resultImage = currentNode?.generatedImage;
  const displaySourceImage = (isGenerating || isError) && pendingSourceImage ? pendingSourceImage : sourceImage;
  const editableImage = isError && pendingSourceImage ? pendingSourceImage : splitSelection === "result" && resultImage ? resultImage : sourceImage;
  const editableImageFile = editableImage ? sourceFilesByAssetId[editableImage.id] ?? null : null;
  const waitingElapsedTime = formatElapsedTime(generationElapsedSeconds);
  const canSubmit = Boolean(
    currentNode && editableImage && prompt.trim() && !isGenerating && (pageStatus !== "success_split" || splitSelection),
  );

  useEffect(() => {
    return () => {
      generationRunIdRef.current += 1;
      activeTaskIdRef.current = null;
    };
  }, []);

  useEffect(() => {
    if (!isGenerating || !generatingStartedAt) {
      setGenerationElapsedSeconds(0);
      return;
    }

    const updateElapsedTime = () => {
      setGenerationElapsedSeconds(Math.max(0, Math.floor((Date.now() - generatingStartedAt) / 1000)));
    };

    updateElapsedTime();
    const intervalId = window.setInterval(updateElapsedTime, 1000);
    return () => window.clearInterval(intervalId);
  }, [generatingStartedAt, isGenerating]);

  useEffect(() => {
    sessionsRef.current = historySessions;
  }, [historySessions]);

  useEffect(() => {
    if (!canUseMockRequestMode) {
      return;
    }
    try {
      window.localStorage.setItem(RETOUCH_REQUEST_MODE_STORAGE_KEY, requestMode);
    } catch {
      // Local storage can be unavailable in private browsing contexts.
    }
  }, [canUseMockRequestMode, requestMode]);

  useEffect(() => {
    let cancelled = false;
    const loadHistory = async () => {
      try {
        const items = await listRetouchHistorySessions();
        if (!cancelled) {
          sessionsRef.current = items;
          setHistorySessions(items);
        }
      } finally {
        if (!cancelled) {
          setIsLoadingHistory(false);
        }
      }
    };

    const handleHistoryChanged = () => {
      void loadHistory();
    };

    void loadHistory();
    window.addEventListener(RETOUCH_HISTORY_CHANGED_EVENT, handleHistoryChanged);
    return () => {
      cancelled = true;
      window.removeEventListener(RETOUCH_HISTORY_CHANGED_EVENT, handleHistoryChanged);
    };
  }, []);

  const persistActiveSession = useCallback(async (draftPrompt = prompt) => {
    if (!activeSessionId) {
      return null;
    }
    const existing = sessionsRef.current.find((session) => session.id === activeSessionId);
    const session = buildRetouchSession(
      activeSessionId,
      existing?.title || "未命名修图",
      existing?.createdAt || new Date().toISOString(),
      draftPrompt,
      existing,
    );
    if (!session) {
      return null;
    }
    sessionsRef.current = [
      session,
      ...sessionsRef.current.filter((item) => item.id !== session.id),
    ].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
    setHistorySessions(sessionsRef.current);
    await saveRetouchHistorySession(session);
    return session;
  }, [activeSessionId, prompt]);

  useEffect(() => {
    if (!activeSessionId || !rootNodeId) {
      return;
    }
    const timeoutId = window.setTimeout(() => {
      void persistActiveSession(prompt);
    }, 600);
    return () => window.clearTimeout(timeoutId);
  }, [activeSessionId, persistActiveSession, prompt, rootNodeId]);
  const handleFile = useCallback(async (file: File | undefined) => {
    if (!file || !file.type.startsWith("image/")) {
      return;
    }

    try {
      await persistActiveSession(prompt);
      const dataUrl = await readFileAsDataUrl(file);
      const rootNode = addRootNode({
        baseImage: createImageAsset(file, dataUrl),
        prompt,
      });
      const sessionId = createRandomId("retouch-session");
      const title = createSessionTitle(file.name, prompt);
      setSourceFilesByAssetId({ [rootNode.baseImage.id]: file });
      setActiveSessionId(sessionId);
      setHasCanvasMarks(false);
      setMarkerColor(null);
      setPendingSourceImage(null);
      setPendingMaskData(undefined);
      setErrorMessage("");
      setSplitSelection(null);
      setPageStatus("editing");

      const session = buildRetouchSession(sessionId, title, new Date().toISOString(), prompt);
      if (session) {
        sessionsRef.current = [session, ...sessionsRef.current.filter((item) => item.id !== session.id)];
        setHistorySessions(sessionsRef.current);
        await saveRetouchHistorySession(session);
      }
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "读取上传图片失败");
      setPageStatus(currentNode ? "error" : "empty");
    }
  }, [addRootNode, currentNode, persistActiveSession, prompt]);

  const handleRemoveImage = useCallback(async () => {
    await persistActiveSession(prompt);
    generationRunIdRef.current += 1;
    activeTaskIdRef.current = null;
    resetTree();
    setActiveSessionId(null);
    setSourceFilesByAssetId({});
    setPrompt("");
    setHasCanvasMarks(false);
    setMarkerColor(null);
    setPendingSourceImage(null);
    setPendingMaskData(undefined);
    setErrorMessage("");
    setSplitSelection(null);
    setPageStatus("empty");
    setGeneratingStartedAt(null);
  }, [persistActiveSession, prompt, resetTree]);

  const handleSelectSplitImage = useCallback((selection: Exclude<SplitSelection, null>) => {
    setSplitSelection(selection);
    setHasCanvasMarks(false);
  }, []);

  const handleDismissError = useCallback(() => {
    setErrorMessage("");
    setPendingSourceImage(null);
    setPendingMaskData(undefined);
    setMarkerColor(null);
    setSplitSelection(null);
    setPageStatus(resultImage ? "success_split" : "editing");
  }, [resultImage]);

  const handleCancelGeneration = useCallback(async () => {
    const taskId = activeTaskIdRef.current;
    generationRunIdRef.current += 1;
    activeTaskIdRef.current = null;

    setErrorMessage("");
    setGeneratingStartedAt(null);
    setPendingSourceImage(null);
    setPendingMaskData(undefined);
    setMarkerColor(null);
    setSplitSelection(null);
    setPageStatus(resultImage ? "success_split" : "editing");

    if (!taskId) {
      return;
    }

    try {
      await cancelCreationTask(taskId);
    } catch {
      setErrorMessage("取消请求失败，已停止本地等待。请稍后在任务队列中确认状态。");
    }
  }, [resultImage]);
  const handleNavigateVersion = useCallback((node: ImageNode) => {
    if (isGenerating) {
      return;
    }

    navigateNode(node.id);
    setErrorMessage("");
    setGeneratingStartedAt(null);
    setPendingSourceImage(null);
    setPendingMaskData(undefined);
    setHasCanvasMarks(false);
    setMarkerColor(null);
    setSplitSelection(null);
    setPageStatus(node.generatedImage ? "success_split" : "editing");
    window.setTimeout(() => void persistActiveSession(prompt), 0);
  }, [isGenerating, navigateNode, persistActiveSession, prompt]);

  const resetWorkspace = useCallback(() => {
    generationRunIdRef.current += 1;
    activeTaskIdRef.current = null;
    resetTree();
    setActiveSessionId(null);
    setSourceFilesByAssetId({});
    setPrompt("");
    setHasCanvasMarks(false);
    setMarkerColor(null);
    setPendingSourceImage(null);
    setPendingMaskData(undefined);
    setErrorMessage("");
    setSplitSelection(null);
    setPageStatus("empty");
    setGeneratingStartedAt(null);
  }, [resetTree]);

  const handleOpenHistorySession = useCallback(async (sessionId: string) => {
    if (isGenerating) {
      return;
    }
    await persistActiveSession(prompt);
    const session = sessionsRef.current.find((item) => item.id === sessionId);
    if (!session) {
      return;
    }

    replaceTree({
      nodesById: session.nodesById,
      rootNodeId: session.rootNodeId,
      currentNodeId: session.currentNodeId,
      nextImageNumber: session.nextImageNumber,
    });
    const restoredNode = session.currentNodeId ? session.nodesById[session.currentNodeId] : null;
    setActiveSessionId(session.id);
    setSourceFilesByAssetId({});
    setPrompt(session.draftPrompt || "");
    setHasCanvasMarks(false);
    setMarkerColor(null);
    setPendingSourceImage(null);
    setPendingMaskData(undefined);
    setErrorMessage("");
    setSplitSelection(null);
    setGeneratingStartedAt(null);
    setPageStatus(restoredNode?.generatedImage ? "success_split" : "editing");
    setIsHistoryOpen(false);
  }, [isGenerating, persistActiveSession, prompt, replaceTree]);

  const handleConfirmDelete = useCallback(async () => {
    const target = deleteConfirm;
    if (!target) {
      return;
    }
    if (target.type === "all") {
      await clearRetouchHistorySessions();
      sessionsRef.current = [];
      setHistorySessions([]);
      resetWorkspace();
    } else {
      await deleteRetouchHistorySession(target.id);
      const nextSessions = sessionsRef.current.filter((session) => session.id !== target.id);
      sessionsRef.current = nextSessions;
      setHistorySessions(nextSessions);
      if (activeSessionId === target.id) {
        resetWorkspace();
      }
    }
    setDeleteConfirm(null);
  }, [activeSessionId, deleteConfirm, resetWorkspace]);
  const handleGenerate = useCallback(async () => {
    const nextPrompt = prompt.trim();
    if (!currentNode || !editableImage || !nextPrompt || isGenerating) {
      return;
    }
    if (pageStatus === "success_split" && !splitSelection) {
      return;
    }

    const retouchPrompt = hasCanvasMarks && markerColor ? buildRetouchPrompt(nextPrompt, markerColor.name) : nextPrompt;
    const lockedSourceImage = editableImage;
    const parentId = currentNode.id;
    const runId = generationRunIdRef.current + 1;
    const clientTaskId = createRandomId("retouch-task");

    generationRunIdRef.current = runId;
    activeTaskIdRef.current = null;
    setPendingSourceImage(lockedSourceImage);
    setPendingMaskData(undefined);
    setErrorMessage("");
    setPageStatus("submitting");
    setGeneratingStartedAt(Date.now());
    setSplitSelection(null);

    const applyTerminalTask = (task: CreationTask) => {
      if (generationRunIdRef.current !== runId) {
        return true;
      }

      if (task.status === "success") {
        try {
          addNode({
            parentId,
            baseImage: lockedSourceImage,
            prompt: nextPrompt,
            generatedImage: createGeneratedAsset(task),
          });
        } catch (error) {
          setErrorMessage(formatCreationTaskErrorMessage(error instanceof Error ? error.message : "生成结果写入失败"));
          setPageStatus("error");
          setGeneratingStartedAt(null);
          return true;
        }
        activeTaskIdRef.current = null;
        setPrompt("");
        setHasCanvasMarks(false);
        setPendingSourceImage(null);
        setPendingMaskData(undefined);
        setPageStatus("success_split");
        setGeneratingStartedAt(null);
        void persistActiveSession("");
        return true;
      }

      if (task.status === "error" || task.status === "cancelled") {
        activeTaskIdRef.current = null;
        setErrorMessage(formatCreationTaskErrorMessage(task.error || (task.status === "cancelled" ? "任务已终止" : "生成失败")));
        setPageStatus("error");
        setGeneratingStartedAt(null);
        return true;
      }

      setPageStatus("polling");
      return false;
    };

    try {
      if (effectiveRequestMode === "mock") {
        await sleep(650);
        if (generationRunIdRef.current !== runId) {
          return;
        }
        applyTerminalTask(createMockImageEditTask(clientTaskId, lockedSourceImage, retouchPrompt));
        return;
      }

      const sourceFile = hasCanvasMarks
        ? await retouchCanvasRef.current?.exportMarkedImage()
        : await imageAssetToFile(lockedSourceImage, sourceFilesByAssetId[lockedSourceImage.id] ?? null);
      if (!sourceFile) {
        throw new Error("标注画布未就绪，请重新提交");
      }
      if (generationRunIdRef.current !== runId) {
        return;
      }

      const submittedTask = await createImageEditTask(
        clientTaskId,
        sourceFile,
        retouchPrompt,
        undefined,
        undefined,
        undefined,
        1,
        undefined,
        "private",
        undefined,
        undefined,
        undefined,
        undefined,
      );
      if (generationRunIdRef.current !== runId) {
        try {
          await cancelCreationTask(submittedTask.id);
        } catch {
          // The user already left this run; best-effort cancellation is enough here.
        }
        return;
      }
      activeTaskIdRef.current = submittedTask.id;

      if (applyTerminalTask(submittedTask)) {
        return;
      }

      while (generationRunIdRef.current === runId) {
        await sleep(CREATION_TASK_POLL_INTERVAL_MS);
        if (generationRunIdRef.current !== runId) {
          return;
        }

        const taskList = await fetchCreationTasks([submittedTask.id]);
        const task = taskList.items.find((item) => item.id === submittedTask.id);
        if (!task) {
          if (taskList.missing_ids.includes(submittedTask.id)) {
            throw new Error("任务状态丢失，请重新提交");
          }
          continue;
        }
        if (applyTerminalTask(task)) {
          return;
        }
      }
    } catch (error) {
      if (generationRunIdRef.current !== runId) {
        return;
      }
      activeTaskIdRef.current = null;
      setErrorMessage(formatCreationTaskErrorMessage(error instanceof Error ? error.message : "提交生成任务失败"));
      setPageStatus("error");
      setGeneratingStartedAt(null);
    }
  }, [addNode, currentNode, editableImage, effectiveRequestMode, hasCanvasMarks, isGenerating, markerColor, pageStatus, persistActiveSession, prompt, sourceFilesByAssetId, splitSelection]);
  const renderImageBadge = (image: ImageTreeAsset, variant: "light" | "dark") => (
    <figcaption
      className={[
        "absolute left-4 top-4 z-20 rounded-full px-3 py-1.5 text-xs font-semibold shadow-sm backdrop-blur",
        variant === "dark" ? "bg-slate-950/82 text-white" : "bg-white/84 text-slate-800",
      ].join(" ")}
    >
      {getImageLabel(image)}
    </figcaption>
  );

  const renderRequestModeToggle = () => (
    <div className="inline-flex h-10 items-center rounded-full bg-white/88 p-1 shadow-sm ring-1 ring-slate-950/10 backdrop-blur" aria-label="Retouch 请求模式">
      {(["api", "mock"] as const).map((mode) => (
        <button
          key={mode}
          type="button"
          disabled={isGenerating}
          onClick={() => setRequestMode(mode)}
          className={[
            "h-8 rounded-full px-3 text-xs font-semibold transition disabled:cursor-not-allowed disabled:opacity-60",
            requestMode === mode ? "bg-slate-950 text-white shadow-sm" : "text-slate-500 hover:bg-slate-100 hover:text-slate-950",
          ].join(" ")}
          title={mode === "api" ? "使用真实 API 生成" : "使用本地 Mock 结果调试页面"}
        >
          {mode === "api" ? "API" : "Mock"}
        </button>
      ))}
    </div>
  );
  const renderDownloadButton = (image: ImageTreeAsset) => (
    <button
      type="button"
      className="absolute bottom-4 right-4 z-30 flex size-10 items-center justify-center rounded-full bg-white/88 text-slate-900 shadow-[0_14px_34px_rgba(15,23,42,0.18)] ring-1 ring-slate-950/10 backdrop-blur transition hover:bg-white hover:scale-105 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[#1456f0]"
      aria-label={`下载 ${getImageLabel(image)}`}
      title={`下载 ${getImageLabel(image)}`}
      onClick={(event) => {
        event.stopPropagation();
        downloadImageAsset(image);
      }}
    >
      <Download className="size-4.5" />
    </button>
  );

  const renderMiniMapNode = (node: ImageNode, depth = 0) => {
    const preview = getNodePreviewImage(node);
    const isCurrent = node.id === currentNodeId;

    return (
      <li key={node.id} className="relative">
        {depth > 0 ? <span className="absolute -left-3 top-0 h-5 w-3 rounded-bl-xl border-b border-l border-slate-300/80" /> : null}
        <div
          className={[
            "group flex w-full items-center gap-2 rounded-2xl p-1.5 transition",
            isCurrent
              ? "bg-slate-950 text-white shadow-[0_14px_32px_rgba(15,23,42,0.18)]"
              : "bg-white/72 text-slate-700 ring-1 ring-slate-950/5 hover:bg-white hover:text-slate-950",
            isGenerating ? "opacity-55" : "",
          ].join(" ")}
        >
          <button
            type="button"
            onClick={() => handleNavigateVersion(node)}
            disabled={isGenerating}
            className="flex min-w-0 flex-1 items-center gap-2 rounded-xl text-left disabled:cursor-not-allowed"
            aria-label={`查看 ${getImageLabel(preview)}`}
            title={`查看 ${getImageLabel(preview)}`}
          >
            <span className="relative size-10 shrink-0 overflow-hidden rounded-xl bg-slate-100 ring-1 ring-black/5">
              <img src={preview.url} alt={preview.name ?? "版本缩略图"} className="size-full object-cover" />
            </span>
            <span className="min-w-0 flex-1">
              <span className="block text-xs font-semibold tabular-nums">{getImageLabel(preview)}</span>
              <span className={isCurrent ? "block truncate text-[11px] text-white/58" : "block truncate text-[11px] text-slate-400"}>
                {node.generatedImage ? "生成版本" : "上传原图"}
              </span>
            </span>
          </button>
          <button
            type="button"
            className={[
              "flex size-8 shrink-0 items-center justify-center rounded-full transition",
              isCurrent ? "bg-white/12 text-white hover:bg-white/20" : "bg-slate-100 text-slate-600 hover:bg-slate-200 hover:text-slate-950",
            ].join(" ")}
            aria-label={`下载 ${getImageLabel(preview)}`}
            title={`下载 ${getImageLabel(preview)}`}
            onClick={(event) => {
              event.stopPropagation();
              downloadImageAsset(preview);
            }}
          >
            <Download className="size-4" />
          </button>
        </div>
        {node.childrenIds.length > 0 ? (
          <ol className="ml-5 mt-2 space-y-2 border-l border-slate-300/70 pl-3">
            {node.childrenIds.map((childId) => {
              const child = nodesById[childId];
              return child ? renderMiniMapNode(child, depth + 1) : null;
            })}
          </ol>
        ) : null}
      </li>
    );
  };

  const renderEditableCanvas = (image: ImageTreeAsset) => (
    <RetouchCanvas
      key={image.id}
      ref={retouchCanvasRef}
      imageFile={editableImageFile}
      imageUrl={image.url}
      brushSize={brushSize}
      onBrushSizeChange={setBrushSize}
      className="size-full min-h-0 flex-1 gap-2 [&>div:first-child]:min-h-0 [&>div:first-child]:rounded-[24px] [&>div:first-child]:border-0 [&>div:first-child]:shadow-[inset_0_0_0_1px_rgba(15,23,42,0.05)]"
      onMarkerChange={(color, hasMarks) => {
        setMarkerColor(hasMarks ? color : null);
        setHasCanvasMarks(hasMarks);
      }}
    />
  );

  return (
    <main
      className="relative flex h-full min-h-0 w-full flex-col overflow-hidden bg-[#f5f7fa] text-slate-950"
    >
      {!currentNode ? (
        <button
          type="button"
          onClick={() => setIsHistoryOpen(true)}
          className="fixed left-3 top-[86px] z-50 inline-flex h-10 items-center gap-2 rounded-full bg-white/88 px-4 text-sm font-medium text-slate-700 shadow-sm ring-1 ring-slate-950/10 backdrop-blur transition hover:bg-white sm:left-5 lg:left-6"
        >
          <History className="size-4" />
          历史记录
          <span className="rounded-full bg-slate-950 px-2 py-0.5 text-[11px] font-semibold text-white">{historySessions.length}</span>
        </button>
      ) : null}

      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={(event) => {
          void handleFile(event.target.files?.[0]);
          event.currentTarget.value = "";
        }}
      />

      <section
        className="flex min-h-0 flex-1 flex-col self-center w-full max-w-[1700px] items-center justify-center px-6 pt-3"
        aria-label="图片编辑画布"
      >
        {currentNode ? (
          <div className="relative flex size-full min-h-0 items-center justify-center [perspective:1600px]">
            {createPortal(
              <aside className="fixed left-3 top-[86px] z-50 flex w-[172px] flex-col gap-2 rounded-[24px] bg-white/82 p-2 shadow-[0_18px_54px_rgba(15,23,42,0.12)] ring-1 ring-slate-950/10 backdrop-blur-xl sm:left-5 lg:left-6" aria-label="Retouch 工具栏">
                <button
                  type="button"
                  onClick={() => setIsHistoryOpen(true)}
                  className="inline-flex h-10 w-full items-center justify-between gap-2 rounded-full bg-white px-3 text-sm font-medium text-slate-700 shadow-sm ring-1 ring-slate-950/10 transition hover:bg-slate-50"
                >
                  <span className="inline-flex items-center gap-2">
                    <History className="size-4" />
                    历史
                  </span>
                  <span className="rounded-full bg-slate-950 px-2 py-0.5 text-[11px] font-semibold text-white">{historySessions.length}</span>
                </button>
                <button
                  type="button"
                  onClick={() => fileInputRef.current?.click()}
                  className="inline-flex h-10 w-full items-center gap-2 rounded-full bg-white px-3 text-sm font-medium text-slate-700 shadow-sm ring-1 ring-slate-950/10 transition hover:bg-slate-50"
                >
                  <ImagePlus className="size-4" />
                  更换图片
                </button>
                <button
                  type="button"
                  onClick={() => void handleRemoveImage()}
                  className="inline-flex h-10 w-full items-center gap-2 rounded-full bg-slate-950 px-3 text-sm font-medium text-white shadow-sm transition hover:bg-slate-800"
                >
                  <Trash2 className="size-4" />
                  移除图片
                </button>
                {canUseMockRequestMode ? (
                  <div className="border-t border-slate-200/80 pt-2">
                    {renderRequestModeToggle()}
                  </div>
                ) : null}
                <div className="flex max-h-[92px] flex-wrap gap-1 overflow-y-auto border-t border-slate-200/80 pt-2">
                  {imageList.map((image) => (
                    <span
                      key={image.id}
                      className="rounded-full bg-slate-950 px-2.5 py-1 text-xs font-semibold tabular-nums text-white"
                    >
                      {getImageLabel(image)}
                    </span>
                  ))}
                </div>
              </aside>,
              document.body,
            )}
            {rootNode ? createPortal(
              <aside className="fixed right-3 top-[86px] z-50 w-[280px] rounded-[24px] bg-white/82 p-3 shadow-[0_24px_80px_rgba(15,23,42,0.16)] ring-1 ring-slate-950/10 backdrop-blur-xl sm:right-5 lg:right-6" aria-label="版本树">
                <div className="mb-3 flex items-center justify-between gap-3 px-1">
                  <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.14em] text-slate-500">
                    <GitBranch className="size-4" />
                    Versions
                  </div>
                  <span className="rounded-full bg-slate-950 px-2 py-1 text-[11px] font-semibold tabular-nums text-white">
                    {imageList.length}
                  </span>
                </div>
                <div className="max-h-[min(48vh,440px)] overflow-y-auto pr-1">
                  <ol className="space-y-2">{renderMiniMapNode(rootNode)}</ol>
                </div>
              </aside>,
              document.body,
            ) : null}
            <AnimatePresence initial={false}>
              {ancestors.map((node, index) => {
                const distance = ancestors.length - index;
                const preview = getNodePreviewImage(node);

                return (
                  <motion.div
                    key={node.id}
                    className="absolute aspect-[16/10] w-[min(78vw,980px)] overflow-hidden rounded-[30px] bg-white/70 shadow-[0_26px_80px_rgba(15,23,42,0.10)] ring-1 ring-white/70"
                    initial={false}
                    animate={{
                      scale: 1 - distance * 0.05,
                      y: `-${distance * 5}%`,
                      opacity: Math.max(0.18, 0.56 - distance * 0.1),
                      zIndex: 10 - distance,
                    }}
                    transition={{ type: "spring", stiffness: 110, damping: 24, mass: 0.9 }}
                    style={{ transformOrigin: "center bottom" }}
                  >
                    <img
                      src={preview.url}
                      alt={preview.name ?? "历史节点预览"}
                      className="size-full object-cover saturate-[0.85]"
                    />
                    <div className="absolute inset-0 bg-white/22 backdrop-blur-[1px]" />
                  </motion.div>
                );
              })}
            </AnimatePresence>

            <motion.div
              key={currentNode.id}
              className="relative z-30 flex size-full min-h-0 max-w-[1500px] items-center justify-center"
              initial={{ opacity: 0, scale: 0.96, y: 28 }}
              animate={{ opacity: 1, scale: 1, y: 0 }}
              transition={{ type: "spring", stiffness: 140, damping: 24 }}
            >
              {(resultImage || isGenerating || isError) && displaySourceImage ? (
                <div className="grid h-[min(74vh,820px)] min-h-[520px] w-full grid-cols-2 gap-4 rounded-[32px] bg-white/72 p-4 shadow-[0_34px_120px_rgba(15,23,42,0.14)] ring-1 ring-white/80 backdrop-blur-xl">
                  <figure
                    className={[
                      "group relative flex min-h-0 overflow-hidden rounded-[24px] bg-slate-100 shadow-[inset_0_0_0_1px_rgba(15,23,42,0.04)] transition duration-200",
                      !isGenerating && !isError && splitSelection === "source" ? "scale-[1.015] ring-2 ring-[#1456f0]" : "ring-1 ring-transparent hover:ring-slate-300",
                    ].join(" ")}
                    onClick={() => {
                      if (!isGenerating && !isError && splitSelection !== "source") {
                        handleSelectSplitImage("source");
                      }
                    }}
                  >
                    {!isGenerating && !isError && splitSelection === "source" ? (
                      renderEditableCanvas(displaySourceImage)
                    ) : (
                      <img
                        src={displaySourceImage.url}
                        alt={displaySourceImage.name ?? "原图"}
                        className="size-full object-contain transition duration-500"
                      />
                    )}
                    {renderImageBadge(displaySourceImage, "light")}
                    {!isGenerating ? renderDownloadButton(displaySourceImage) : null}
                    {!isGenerating && !isError && splitSelection === "source" ? (
                      <span className="absolute right-4 top-4 z-20 inline-flex items-center gap-1 rounded-full bg-[#1456f0] px-3 py-1.5 text-xs font-semibold text-white shadow-sm">
                        <Check className="size-3.5" />
                        正在编辑
                      </span>
                    ) : null}
                  </figure>
                  <figure
                    className={[
                      "group relative flex min-h-0 overflow-hidden rounded-[24px] bg-slate-100 shadow-[0_18px_60px_rgba(15,23,42,0.10)] transition duration-200",
                      !isGenerating && !isError && splitSelection === "result" ? "scale-[1.015] ring-2 ring-[#1456f0]" : "ring-1 ring-transparent hover:ring-slate-300",
                    ].join(" ")}
                    onClick={() => {
                      if (resultImage && !isGenerating && !isError && splitSelection !== "result") {
                        handleSelectSplitImage("result");
                      }
                    }}
                  >
                    {isError ? (
                      <div className="flex size-full flex-col items-center justify-center bg-[radial-gradient(circle_at_50%_28%,rgba(239,68,68,0.14),transparent_34%),linear-gradient(135deg,#fff7f7,#f8fafc)] px-8 text-center">
                        <div className="relative flex size-20 items-center justify-center rounded-full bg-white text-red-600 shadow-[0_18px_50px_rgba(127,29,29,0.16)] ring-1 ring-red-200/70">
                          <div className="absolute inset-2 rounded-full border border-red-100" />
                          <AlertTriangle className="size-8" />
                        </div>
                        <div className="mt-7 text-sm font-medium uppercase tracking-[0.18em] text-red-400">
                          Generation failed
                        </div>
                        <div className="mt-2 max-w-md text-2xl font-semibold tracking-normal text-slate-950">
                          本轮没有生成新图片
                        </div>
                        <div className="mt-3 max-w-md text-sm leading-6 text-slate-500">
                          {errorMessage || "生成失败，请保留当前内容后重试。"}
                        </div>
                        <div className="mt-7 flex flex-wrap items-center justify-center gap-3">
                          <button
                            type="button"
                            onClick={handleGenerate}
                            disabled={!canSubmit}
                            className="inline-flex h-11 items-center gap-2 rounded-full bg-slate-950 px-5 text-sm font-medium text-white shadow-sm transition hover:bg-slate-800 disabled:cursor-not-allowed disabled:bg-slate-300"
                          >
                            <RotateCcw className="size-4" />
                            重试
                          </button>
                          <button
                            type="button"
                            onClick={handleDismissError}
                            className="inline-flex h-11 items-center gap-2 rounded-full bg-white px-5 text-sm font-medium text-slate-700 shadow-sm ring-1 ring-slate-950/10 transition hover:bg-slate-50"
                          >
                            <X className="size-4" />
                            继续编辑
                          </button>
                        </div>
                      </div>
                    ) : !isGenerating && resultImage ? (
                      splitSelection === "result" ? (
                        renderEditableCanvas(resultImage)
                      ) : (
                        <img
                          src={resultImage.url}
                          alt={resultImage.name ?? "生成图"}
                          className="size-full object-contain transition duration-500"
                        />
                      )
                    ) : (
                      <div className="flex size-full flex-col items-center justify-center bg-[radial-gradient(circle_at_50%_38%,rgba(15,23,42,0.08),transparent_34%),linear-gradient(135deg,#f8fafc,#eef2f7)] px-8 text-center">
                        <div className="relative flex size-20 items-center justify-center rounded-full bg-white shadow-[0_18px_50px_rgba(15,23,42,0.12)] ring-1 ring-slate-950/5">
                          <div className="absolute inset-2 rounded-full border border-slate-200" />
                          <Loader2 className="size-8 animate-spin text-slate-950" />
                        </div>
                        <div className="mt-7 text-sm font-medium uppercase tracking-[0.18em] text-slate-400">
                          Generating
                        </div>
                        <div className="mt-2 text-4xl font-semibold tabular-nums tracking-normal text-slate-950">
                          {waitingElapsedTime}
                        </div>
                        <div className="mt-3 max-w-xs text-sm leading-6 text-slate-500">
                          正在等待生成结果，完成后会作为新图片加入编号列表。
                        </div>
                        <button
                          type="button"
                          onClick={handleCancelGeneration}
                          className="mt-7 inline-flex h-11 items-center gap-2 rounded-full bg-white px-5 text-sm font-medium text-slate-700 shadow-sm ring-1 ring-slate-950/10 transition hover:bg-slate-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[#1456f0]"
                        >
                          <X className="size-4" />
                          取消
                        </button>
                      </div>
                    )}
                    {!isGenerating && !isError && resultImage ? renderImageBadge(resultImage, "dark") : null}
                    {!isGenerating && !isError && resultImage ? renderDownloadButton(resultImage) : null}
                    {!isGenerating && !isError && splitSelection === "result" ? (
                      <span className="absolute right-4 top-4 z-20 inline-flex items-center gap-1 rounded-full bg-[#1456f0] px-3 py-1.5 text-xs font-semibold text-white shadow-sm">
                        <Check className="size-3.5" />
                        正在编辑
                      </span>
                    ) : null}
                  </figure>
                </div>
              ) : sourceImage ? (
                <div className="relative flex min-h-0 w-full flex-1 flex-col self-stretch rounded-[32px] bg-white/72 p-4 shadow-[0_34px_120px_rgba(15,23,42,0.12)] ring-1 ring-white/80 backdrop-blur-xl">
                  <span className="absolute left-8 top-8 z-20 rounded-full bg-white/84 px-3 py-1.5 text-xs font-semibold text-slate-800 shadow-sm backdrop-blur">
                    {getImageLabel(sourceImage)}
                  </span>
                  {renderEditableCanvas(sourceImage)}
                </div>
              ) : null}
            </motion.div>
          </div>
        ) : (
          <button
            type="button"
            onClick={() => fileInputRef.current?.click()}
            onDragEnter={(event) => {
              event.preventDefault();
              setIsDragging(true);
            }}
            onDragOver={(event) => {
              event.preventDefault();
              setIsDragging(true);
            }}
            onDragLeave={(event) => {
              event.preventDefault();
              setIsDragging(false);
            }}
            onDrop={(event) => {
              event.preventDefault();
              setIsDragging(false);
              void handleFile(event.dataTransfer.files?.[0]);
            }}
            className={[
              "group flex aspect-[16/10] w-full max-w-5xl flex-col items-center justify-center rounded-[28px]",
              "bg-white/78 p-8 text-center shadow-[0_24px_80px_rgba(15,23,42,0.06)] backdrop-blur",
              "outline outline-1 -outline-offset-1 outline-dashed transition duration-200",
              isDragging
                ? "outline-[#1456f0] ring-4 ring-[#1456f0]/10"
                : "outline-slate-300 hover:outline-slate-400",
            ].join(" ")}
          >
            <span className="flex size-20 items-center justify-center rounded-full bg-slate-950 text-white shadow-[0_18px_40px_rgba(15,23,42,0.16)] transition group-hover:scale-105">
              <Upload className="size-8" />
            </span>
            <span className="mt-7 text-2xl font-semibold tracking-normal text-slate-950">
              拖入一张图片开始编辑
            </span>
            <span className="mt-3 text-sm leading-6 text-slate-500">
              或点击选择文件。上传后会登记为 #1。
            </span>
          </button>
        )}
      </section>

      <Dialog open={isHistoryOpen} onOpenChange={setIsHistoryOpen}>
        <DialogContent className="flex h-[min(82dvh,760px)] w-[92vw] max-w-[520px] flex-col overflow-hidden rounded-[28px] border-white/80 bg-white p-0 shadow-[0_32px_110px_-38px_rgba(15,23,42,0.45)]">
          <DialogHeader className="border-b border-slate-100 px-6 pt-6 pb-4">
            <DialogTitle className="flex items-center gap-2 text-xl font-bold tracking-tight text-slate-950">
              <History className="size-5" />
              Retouch 历史
            </DialogTitle>
            <DialogDescription className="text-sm leading-6 text-slate-500">
              保存当前浏览器内的修图项目，打开后会恢复版本树和输入框 prompt。
            </DialogDescription>
          </DialogHeader>
          <div className="flex items-center justify-between gap-3 border-b border-slate-100 px-6 py-3">
            <div className="text-xs font-semibold uppercase tracking-[0.14em] text-slate-400">
              {historySessions.length} Projects
            </div>
            <button
              type="button"
              disabled={historySessions.length === 0}
              onClick={() => setDeleteConfirm({ type: "all" })}
              className="inline-flex h-9 items-center gap-2 rounded-full bg-white px-3 text-xs font-semibold text-rose-600 ring-1 ring-rose-100 transition hover:bg-rose-50 disabled:cursor-not-allowed disabled:text-slate-300 disabled:ring-slate-100"
            >
              <Trash2 className="size-3.5" />
              清空
            </button>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
            {isLoadingHistory ? (
              <div className="flex items-center gap-2 px-2 py-4 text-sm text-slate-500">
                <Loader2 className="size-4 animate-spin" />
                正在读取历史记录
              </div>
            ) : historySessions.length === 0 ? (
              <div className="flex h-52 flex-col items-center justify-center rounded-3xl border border-dashed border-slate-200 bg-slate-50/70 px-6 text-center">
                <History className="size-8 text-slate-300" />
                <div className="mt-4 text-sm font-semibold text-slate-700">还没有 Retouch 历史</div>
                <div className="mt-1 text-xs leading-5 text-slate-400">上传图片并生成版本后，会在这里保留项目入口。</div>
              </div>
            ) : (
              <div className="space-y-2">
                {historySessions.map((session) => {
                  const stats = getRetouchHistoryStats(session);
                  const previewUrl = session.thumbnailUrl || getRetouchSessionPreviewUrl(session);
                  const active = session.id === activeSessionId;
                  return (
                    <div
                      key={session.id}
                      className={[
                        "group relative flex gap-3 rounded-2xl border p-2 transition",
                        active
                          ? "border-slate-950/10 bg-slate-950 text-white shadow-[0_14px_40px_rgba(15,23,42,0.18)]"
                          : "border-transparent bg-slate-50 text-slate-800 hover:border-slate-200 hover:bg-white",
                      ].join(" ")}
                    >
                      <button
                        type="button"
                        disabled={isGenerating}
                        onClick={() => void handleOpenHistorySession(session.id)}
                        className="flex min-w-0 flex-1 items-center gap-3 text-left disabled:cursor-not-allowed disabled:opacity-60"
                      >
                        <span className="size-16 shrink-0 overflow-hidden rounded-xl bg-slate-200 ring-1 ring-black/5">
                          {previewUrl ? (
                            <img src={previewUrl} alt={session.title} className="size-full object-cover" />
                          ) : null}
                        </span>
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-sm font-semibold">{session.title}</span>
                          <span className={active ? "mt-1 block text-xs text-white/60" : "mt-1 block text-xs text-slate-500"}>
                            {stats.imageCount} 张图 · {stats.editCount} 次修图 · {formatHistoryTime(session.updatedAt)}
                          </span>
                          {session.draftPrompt ? (
                            <span className={active ? "mt-1 block truncate text-[11px] text-white/45" : "mt-1 block truncate text-[11px] text-slate-400"}>
                              {session.draftPrompt}
                            </span>
                          ) : null}
                        </span>
                      </button>
                      <button
                        type="button"
                        onClick={() => setDeleteConfirm({ type: "one", id: session.id })}
                        className={[
                          "absolute right-3 top-3 inline-flex size-8 items-center justify-center rounded-full opacity-0 transition group-hover:opacity-100",
                          active ? "bg-white/10 text-white hover:bg-white/20" : "bg-white text-slate-400 shadow-sm hover:text-rose-600",
                        ].join(" ")}
                        aria-label={`删除 ${session.title}`}
                      >
                        <Trash2 className="size-4" />
                      </button>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </DialogContent>
      </Dialog>

      {deleteConfirm ? (
        <Dialog open onOpenChange={(open) => (!open ? setDeleteConfirm(null) : null)}>
          <DialogContent showCloseButton={false} className="rounded-2xl p-6">
            <DialogHeader className="gap-2">
              <DialogTitle>{deleteConfirm.type === "all" ? "清空 Retouch 历史" : "删除 Retouch 项目"}</DialogTitle>
              <DialogDescription className="text-sm leading-6">
                {deleteConfirm.type === "all"
                  ? "这会删除当前账号在本浏览器内保存的所有 Retouch 历史，当前工作台也会清空。"
                  : "这会删除这个 Retouch 项目的版本树和本地历史入口。"}
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <button
                type="button"
                onClick={() => setDeleteConfirm(null)}
                className="inline-flex h-10 items-center justify-center rounded-full bg-white px-4 text-sm font-medium text-slate-700 ring-1 ring-slate-200 transition hover:bg-slate-50"
              >
                取消
              </button>
              <button
                type="button"
                onClick={() => void handleConfirmDelete()}
                className="inline-flex h-10 items-center justify-center rounded-full bg-rose-600 px-4 text-sm font-medium text-white transition hover:bg-rose-700"
              >
                确认删除
              </button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}

      <form
        className="self-center mb-4 flex w-[min(calc(100%-32px),760px)] shrink-0 items-center gap-3 rounded-full bg-white/90 p-2 pl-5 shadow-[0_24px_70px_rgba(15,23,42,0.16)] ring-1 ring-slate-950/5 backdrop-blur-xl"
        onSubmit={(event) => {
          event.preventDefault();
          handleGenerate();
        }}
      >
        <input
          value={prompt}
          onChange={(event) => setPrompt(event.target.value)}
          disabled={isGenerating}
          className="h-12 min-w-0 flex-1 bg-transparent text-[15px] leading-none text-slate-950 outline-none placeholder:text-slate-400 disabled:text-slate-400"
          placeholder={pageStatus === "success_split" && !splitSelection ? "选择左图或右图后继续输入修改要求" : "一句话描述你想修改的内容"}
          aria-label="编辑指令"
        />
        <button
          type="submit"
          className="flex size-12 shrink-0 items-center justify-center rounded-full bg-slate-950 text-white transition hover:bg-slate-800 disabled:cursor-not-allowed disabled:bg-slate-300"
          disabled={!canSubmit}
          aria-label="发送指令"
        >
          {isGenerating ? (
            <Loader2 className="size-5 animate-spin" />
          ) : (
            <Send className="size-5" />
          )}
        </button>
      </form>
    </main>
  );
}





