"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type WheelEvent } from "react";
import { ImagePlus, Loader2, Send, Trash2, Upload } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";

import {
  RetouchCanvas,
  type RetouchCanvasHandle,
} from "@/app/retouch/components/retouch-canvas";
import {
  useImageTreeStore,
  type ImageNode,
  type ImageTreeAsset,
} from "@/store/useImageTreeStore";

function createImageAsset(file: File): ImageTreeAsset {
  const random =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(16).slice(2)}`;

  return {
    id: `upload-${random}`,
    url: URL.createObjectURL(file),
    name: file.name,
  };
}

function createMockGeneratedAsset(prompt: string): ImageTreeAsset {
  const random =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(16).slice(2)}`;

  return {
    id: `generated-${random}`,
    url: `https://source.unsplash.com/1200x900/?abstract,portrait,studio&sig=${encodeURIComponent(random)}`,
    name: prompt.trim() || "生成结果",
  };
}

function getNodePreviewImage(node: ImageNode) {
  return node.generatedImage ?? node.baseImage;
}

function readFileAsDataUrl(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error ?? new Error("读取标注图失败"));
    reader.readAsDataURL(file);
  });
}

export default function EditorPage() {
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const retouchCanvasRef = useRef<RetouchCanvasHandle | null>(null);
  const generationTimerRef = useRef<number | null>(null);
  const wheelLockRef = useRef(0);
  const [prompt, setPrompt] = useState("");
  const [isDragging, setIsDragging] = useState(false);
  const [isGenerating, setIsGenerating] = useState(false);
  const [hasCanvasMarks, setHasCanvasMarks] = useState(false);
  const [brushSize, setBrushSize] = useState(5);
  const [sourceFilesByNodeId, setSourceFilesByNodeId] = useState<Record<string, File>>({});
  const addRootNode = useImageTreeStore((state) => state.addRootNode);
  const addNode = useImageTreeStore((state) => state.addNode);
  const navigateNode = useImageTreeStore((state) => state.navigateNode);
  const getAncestors = useImageTreeStore((state) => state.getAncestors);
  const resetTree = useImageTreeStore((state) => state.resetTree);
  const currentNodeId = useImageTreeStore((state) => state.currentNodeId);
  const currentNode = useImageTreeStore((state) =>
    state.currentNodeId ? state.nodesById[state.currentNodeId] : null,
  );
  const ancestors = useMemo(
    () => (currentNodeId ? getAncestors(currentNodeId) : []),
    [currentNodeId, getAncestors],
  );
  const baseImage = currentNode?.baseImage;
  const sourceImageFile = currentNode ? sourceFilesByNodeId[currentNode.id] ?? null : null;

  useEffect(() => {
    return () => {
      if (generationTimerRef.current) {
        window.clearTimeout(generationTimerRef.current);
      }
    };
  }, []);

  const handleFile = useCallback((file: File | undefined) => {
    if (!file || !file.type.startsWith("image/")) {
      return;
    }

    const rootNode = addRootNode({
      baseImage: createImageAsset(file),
      prompt,
    });
    setSourceFilesByNodeId({ [rootNode.id]: file });
    setHasCanvasMarks(false);
  }, [addRootNode, prompt]);

  const handleRemoveImage = useCallback(() => {
    if (generationTimerRef.current) {
      window.clearTimeout(generationTimerRef.current);
      generationTimerRef.current = null;
    }
    resetTree();
    setSourceFilesByNodeId({});
    setPrompt("");
    setHasCanvasMarks(false);
    setIsGenerating(false);
  }, [resetTree]);

  const handleGenerate = useCallback(() => {
    const nextPrompt = prompt.trim();
    if (!currentNode || !nextPrompt || isGenerating) {
      return;
    }

    setIsGenerating(true);
    const parentId = currentNode.id;

    generationTimerRef.current = window.setTimeout(async () => {
      generationTimerRef.current = null;
      let maskData: string | undefined;

      if (hasCanvasMarks && retouchCanvasRef.current) {
        try {
          const markedFile = await retouchCanvasRef.current.exportMarkedImage();
          maskData = await readFileAsDataUrl(markedFile);
        } catch {
          maskData = undefined;
        }
      }

      try {
        addNode({
          parentId,
          prompt: nextPrompt,
          generatedImage: createMockGeneratedAsset(nextPrompt),
          maskData,
        });
      } catch {
        setIsGenerating(false);
        return;
      }
      setPrompt("");
      setIsGenerating(false);
    }, 1000);
  }, [addNode, currentNode, hasCanvasMarks, isGenerating, prompt]);

  const handleWheel = useCallback((event: WheelEvent<HTMLElement>) => {
    if (!currentNode) {
      return;
    }

    const now = window.performance.now();
    if (now - wheelLockRef.current < 700 || Math.abs(event.deltaY) < 24) {
      return;
    }

    if (event.deltaY < 0 && currentNode.parentId) {
      event.preventDefault();
      wheelLockRef.current = now;
      navigateNode(currentNode.parentId);
      return;
    }

    if (event.deltaY > 0 && currentNode.childrenIds.length > 0) {
      event.preventDefault();
      wheelLockRef.current = now;
      navigateNode(currentNode.childrenIds[0]);
    }
  }, [currentNode, navigateNode]);

  return (
    <main
      className="relative flex h-full min-h-0 w-full flex-col overflow-hidden bg-[#f5f7fa] text-slate-950"
      onWheel={handleWheel}
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
        className="flex min-h-0 flex-1 flex-col self-center w-full max-w-7xl items-center justify-center px-6 pt-6"
        aria-label="图片编辑画布"
      >
        {currentNode ? (
          <div className="relative flex size-full min-h-0 items-center justify-center [perspective:1600px]">
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
              className="relative z-30 flex size-full min-h-0 max-w-6xl items-center justify-center"
              initial={{ opacity: 0, scale: 0.96, y: 28 }}
              animate={{ opacity: 1, scale: 1, y: 0 }}
              transition={{ type: "spring", stiffness: 140, damping: 24 }}
            >
              {currentNode.generatedImage && baseImage ? (
                <div className="grid aspect-[16/9] max-h-full w-full grid-cols-2 gap-4 rounded-[32px] bg-white/72 p-4 shadow-[0_34px_120px_rgba(15,23,42,0.14)] ring-1 ring-white/80 backdrop-blur-xl">
                  <figure className="group relative overflow-hidden rounded-[24px] bg-slate-100 shadow-[inset_0_0_0_1px_rgba(15,23,42,0.04)]">
                    <img
                      src={baseImage.url}
                      alt={baseImage.name ?? "原图"}
                      className="size-full object-cover transition duration-500 group-hover:scale-[1.02]"
                    />
                    <figcaption className="absolute left-4 top-4 rounded-full bg-white/82 px-3 py-1.5 text-xs font-medium tracking-[0.02em] text-slate-700 shadow-sm backdrop-blur">
                      Base Image
                    </figcaption>
                  </figure>
                  <figure className="group relative overflow-hidden rounded-[24px] bg-slate-100 shadow-[0_18px_60px_rgba(15,23,42,0.10)]">
                    <img
                      src={currentNode.generatedImage.url}
                      alt={currentNode.generatedImage.name ?? "生成图"}
                      className="size-full object-cover transition duration-500 group-hover:scale-[1.02]"
                    />
                    <figcaption className="absolute left-4 top-4 rounded-full bg-slate-950/82 px-3 py-1.5 text-xs font-medium tracking-[0.02em] text-white shadow-sm backdrop-blur">
                      Generated Image
                    </figcaption>
                  </figure>
                </div>
              ) : (
                <div className="relative flex min-h-0 w-full flex-1 flex-col self-stretch rounded-[32px] bg-white/72 p-4 shadow-[0_34px_120px_rgba(15,23,42,0.12)] ring-1 ring-white/80 backdrop-blur-xl">
                  <RetouchCanvas
                    ref={retouchCanvasRef}
                    imageFile={sourceImageFile}
                    imageUrl={currentNode.baseImage.url}
                    brushSize={brushSize}
                    onBrushSizeChange={setBrushSize}
                    className="min-h-0 flex-1 [&>div:first-child]:min-h-0 [&>div:first-child]:rounded-[24px] [&>div:first-child]:border-0 [&>div:first-child]:shadow-[inset_0_0_0_1px_rgba(15,23,42,0.05)]"
                    onMarkerChange={(_, hasMarks) => setHasCanvasMarks(hasMarks)}
                  />
                </div>
              )}
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
            <span className="mt-7 text-2xl font-semibold tracking-[-0.02em] text-slate-950">
              拖入一张图片开始编辑
            </span>
            <span className="mt-3 text-sm leading-6 text-slate-500">
              或点击选择文件。当前阶段只搭建上传入口与指令输入。
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
          className="h-12 min-w-0 flex-1 bg-transparent text-[15px] leading-none text-slate-950 outline-none placeholder:text-slate-400"
          placeholder="一句话描述你想修改的内容"
          aria-label="编辑指令"
        />
        <button
          type="submit"
          className="flex size-12 shrink-0 items-center justify-center rounded-full bg-slate-950 text-white transition hover:bg-slate-800 disabled:cursor-not-allowed disabled:bg-slate-300"
          disabled={!currentNode || !prompt.trim() || isGenerating}
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
