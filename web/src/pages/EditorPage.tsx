"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { AlertTriangle, Check, Download, GitBranch, ImagePlus, Loader2, RotateCcw, Send, Trash2, Upload, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";

import {
  RetouchCanvas,
  type RetouchCanvasHandle,
} from "@/app/retouch/components/retouch-canvas";
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

type PageStatus = "empty" | "editing" | "submitting" | "polling" | "success_split" | "error";
type SplitSelection = "source" | "result" | null;

const CREATION_TASK_POLL_INTERVAL_MS = 2000;

function createRandomId(prefix: string) {
  const random =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `${prefix}-${random}`;
}

function createImageAsset(file: File): ImageTreeAsset {
  return {
    id: createRandomId("upload"),
    url: URL.createObjectURL(file),
    name: file.name,
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

export default function EditorPage() {
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const retouchCanvasRef = useRef<RetouchCanvasHandle | null>(null);
  const generationRunIdRef = useRef(0);
  const activeTaskIdRef = useRef<string | null>(null);
  const [prompt, setPrompt] = useState("");
  const [pageStatus, setPageStatus] = useState<PageStatus>("empty");
  const [splitSelection, setSplitSelection] = useState<SplitSelection>(null);
  const [isDragging, setIsDragging] = useState(false);
  const [generatingStartedAt, setGeneratingStartedAt] = useState<number | null>(null);
  const [generationElapsedSeconds, setGenerationElapsedSeconds] = useState(0);
  const [hasCanvasMarks, setHasCanvasMarks] = useState(false);
  const [pendingSourceImage, setPendingSourceImage] = useState<ImageTreeAsset | null>(null);
  const [pendingMaskData, setPendingMaskData] = useState<string | undefined>();
  const [errorMessage, setErrorMessage] = useState("");
  const [brushSize, setBrushSize] = useState(5);
  const [sourceFilesByAssetId, setSourceFilesByAssetId] = useState<Record<string, File>>({});
  const addRootNode = useImageTreeStore((state) => state.addRootNode);
  const addNode = useImageTreeStore((state) => state.addNode);
  const navigateNode = useImageTreeStore((state) => state.navigateNode);
  const getAncestors = useImageTreeStore((state) => state.getAncestors);
  const resetTree = useImageTreeStore((state) => state.resetTree);
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

  const handleFile = useCallback((file: File | undefined) => {
    if (!file || !file.type.startsWith("image/")) {
      return;
    }

    const rootNode = addRootNode({
      baseImage: createImageAsset(file),
      prompt,
    });
    setSourceFilesByAssetId({ [rootNode.baseImage.id]: file });
    setHasCanvasMarks(false);
    setPendingSourceImage(null);
    setPendingMaskData(undefined);
    setErrorMessage("");
    setSplitSelection(null);
    setPageStatus("editing");
  }, [addRootNode, prompt]);

  const handleRemoveImage = useCallback(() => {
    generationRunIdRef.current += 1;
    activeTaskIdRef.current = null;
    resetTree();
    setSourceFilesByAssetId({});
    setPrompt("");
    setHasCanvasMarks(false);
    setPendingSourceImage(null);
    setPendingMaskData(undefined);
    setErrorMessage("");
    setSplitSelection(null);
    setPageStatus("empty");
    setGeneratingStartedAt(null);
  }, [resetTree]);

  const handleSelectSplitImage = useCallback((selection: Exclude<SplitSelection, null>) => {
    setSplitSelection(selection);
    setHasCanvasMarks(false);
  }, []);

  const handleDismissError = useCallback(() => {
    setErrorMessage("");
    setPendingSourceImage(null);
    setPendingMaskData(undefined);
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
    setSplitSelection(null);
    setPageStatus(node.generatedImage ? "success_split" : "editing");
  }, [isGenerating, navigateNode]);

  const handleGenerate = useCallback(async () => {
    const nextPrompt = prompt.trim();
    if (!currentNode || !editableImage || !nextPrompt || isGenerating) {
      return;
    }
    if (pageStatus === "success_split" && !splitSelection) {
      return;
    }

    const lockedSourceImage = editableImage;
    const parentId = currentNode.id;
    const runId = generationRunIdRef.current + 1;
    const clientTaskId = createRandomId("retouch-task");
    let maskData: string | undefined = pageStatus === "error" ? pendingMaskData : undefined;

    if (pageStatus !== "error" && hasCanvasMarks && retouchCanvasRef.current) {
      try {
        maskData = retouchCanvasRef.current.exportMaskDataUrl();
      } catch (error) {
        setErrorMessage(formatCreationTaskErrorMessage(error instanceof Error ? error.message : "导出 mask 失败"));
        setPendingSourceImage(lockedSourceImage);
        setPendingMaskData(undefined);
        setPageStatus("error");
        return;
      }
    }

    generationRunIdRef.current = runId;
    activeTaskIdRef.current = null;
    setPendingSourceImage(lockedSourceImage);
    setPendingMaskData(maskData);
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
            maskData,
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
      const sourceFile = await imageAssetToFile(lockedSourceImage, sourceFilesByAssetId[lockedSourceImage.id] ?? null);
      if (generationRunIdRef.current !== runId) {
        return;
      }

      const submittedTask = await createImageEditTask(
        clientTaskId,
        sourceFile,
        nextPrompt,
        undefined,
        undefined,
        undefined,
        1,
        undefined,
        "private",
        undefined,
        undefined,
        undefined,
        maskData ? { inputImageMask: maskData } : undefined,
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
  }, [addNode, currentNode, editableImage, hasCanvasMarks, isGenerating, pageStatus, pendingMaskData, prompt, sourceFilesByAssetId, splitSelection]);
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
      onMarkerChange={(_, hasMarks) => setHasCanvasMarks(hasMarks)}
    />
  );

  return (
    <main
      className="relative flex h-full min-h-0 w-full flex-col overflow-hidden bg-[#f5f7fa] text-slate-950"
    >
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={(event) => {
          handleFile(event.target.files?.[0]);
          event.currentTarget.value = "";
        }}
      />

      <section
        className="flex min-h-0 flex-1 flex-col self-center w-full max-w-[1700px] items-center justify-center px-6 pt-3"
        aria-label="图片编辑画布"
      >
        {currentNode ? (
          <div className="relative flex size-full min-h-0 items-center justify-center [perspective:1600px]">
            <div className="absolute left-6 top-0 z-50 flex max-w-[52vw] items-center gap-2 overflow-hidden rounded-full bg-white/78 px-2 py-1 shadow-sm ring-1 ring-slate-950/10 backdrop-blur">
              {imageList.map((image) => (
                <span
                  key={image.id}
                  className="rounded-full bg-slate-950 px-3 py-1 text-xs font-semibold tabular-nums text-white"
                >
                  {getImageLabel(image)}
                </span>
              ))}
            </div>
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
            <div className="absolute right-6 top-0 z-50 flex items-center gap-2">
              <button
                type="button"
                onClick={() => fileInputRef.current?.click()}
                className="inline-flex h-10 items-center gap-2 rounded-full bg-white/88 px-4 text-sm font-medium text-slate-700 shadow-sm ring-1 ring-slate-950/10 backdrop-blur transition hover:bg-white"
              >
                <ImagePlus className="size-4" />
                更换图片
              </button>
              <button
                type="button"
                onClick={handleRemoveImage}
                className="inline-flex h-10 items-center gap-2 rounded-full bg-slate-950/88 px-4 text-sm font-medium text-white shadow-sm backdrop-blur transition hover:bg-slate-800"
              >
                <Trash2 className="size-4" />
                移除
              </button>
            </div>
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
                        className="size-full object-cover transition duration-500 group-hover:scale-[1.02]"
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
                          className="size-full object-cover transition duration-500 group-hover:scale-[1.02]"
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
              handleFile(event.dataTransfer.files?.[0]);
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





