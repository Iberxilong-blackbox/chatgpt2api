"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Brush,
  Download,
  Eye,
  ImagePlus,
  LoaderCircle,
  MessageSquarePlus,
  Send,
  ShieldCheck,
  Sparkles,
  Upload,
} from "lucide-react";
import { toast } from "sonner";

import { RetouchCanvas, type RetouchCanvasHandle } from "@/app/retouch/components/retouch-canvas";
import { buildRetouchPrompt, type RetouchMarkerColor } from "@/app/retouch/retouch-prompt";
import { AuthenticatedImage } from "@/components/authenticated-image";
import { ImageLightbox } from "@/components/image-lightbox";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  createImageEditTask,
  DEFAULT_IMAGE_MODEL,
  fetchCreationTasks,
  IMAGE_CREATION_MODEL_OPTIONS,
  IMAGE_OUTPUT_FORMAT_OPTIONS,
  isImageOutputFormat,
  type CreationTask,
  type ImageModel,
  type ImageOutputFormat,
  type ImageVisibility,
} from "@/lib/api";
import {
  fetchAuthenticatedImageBlob,
  shouldUseAuthenticatedImageFallback,
} from "@/lib/authenticated-image";
import { getManagedImagePathFromUrl } from "@/lib/image-path";
import { useAuthGuard } from "@/lib/use-auth-guard";
import { cn } from "@/lib/utils";
import {
  saveImageConversation,
  type ImageConversation,
  type ImageTurn,
  type ImageTurnStatus,
  type StoredImage,
} from "@/store/image-conversations";
import { clearImageTurnProgress, setImageTurnProgress } from "@/store/image-turn-progress";

type CreationTaskDataItem = NonNullable<CreationTask["data"]>[number];
type RetouchLightboxImage = {
  id: string;
  src: string;
  fileName?: string;
  outputFormat?: string;
  dimensions?: string;
};

type SubmitState = {
  conversation: ImageConversation | null;
  status: "idle" | "submitting" | "polling" | "done" | "error";
};

const DEFAULT_MARKER: RetouchMarkerColor = { id: "red", name: "红色", css: "#ff1f1f" };
const RETOUCH_OUTPUT_FORMAT: ImageOutputFormat = "png";
const POLL_INTERVAL_MS = 2500;
const MAX_POLL_ATTEMPTS = 180;

function createClientId(prefix: string) {
  const random = typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `${prefix}-${random}`;
}

function sleep(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function fileToDataUrl(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(reader.error ?? new Error("读取图片失败"));
    reader.onload = () => resolve(String(reader.result || ""));
    reader.readAsDataURL(file);
  });
}


function dataUrlToBlob(dataUrl: string) {
  const [meta, data] = dataUrl.split(",");
  const mime = meta?.match(/^data:(.*?);base64$/)?.[1] || "image/png";
  const binary = atob(data || "");
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return new Blob([bytes], { type: mime });
}
function positiveDimension(value: unknown) {
  const numeric = Number(value);
  return Number.isFinite(numeric) && numeric > 0 ? numeric : undefined;
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

function formatCreationTaskError(error: unknown, fallback = "提交任务失败") {
  return formatCreationTaskErrorMessage(error instanceof Error ? error.message : String(error || fallback));
}

function creationTaskImageStatus(task: CreationTask, dataIndex = 0) {
  const outputStatus = task.output_statuses?.[dataIndex];
  if (outputStatus === "queued" || outputStatus === "running" || outputStatus === "success" || outputStatus === "error" || outputStatus === "cancelled") {
    return outputStatus;
  }
  if (task.status === "queued" || task.status === "running" || task.status === "success" || task.status === "error" || task.status === "cancelled") {
    return task.status;
  }
  return undefined;
}

function updateStoredImage(image: StoredImage, updates: Partial<StoredImage>): StoredImage {
  return { ...image, ...updates };
}

function taskDataToStoredImage(image: StoredImage, task: CreationTask, dataIndex = 0, fallbackVisibility?: ImageVisibility): StoredImage {
  const taskVisibility = task.visibility || fallbackVisibility || image.visibility || "private";
  const successUpdates = (item: CreationTaskDataItem) => {
    const width = positiveDimension(item.width);
    const height = positiveDimension(item.height);
    return {
      taskId: task.id,
      taskStatus: "success" as const,
      status: "success" as const,
      b64_json: item.b64_json,
      url: item.url,
      path: item.url ? getManagedImagePathFromUrl(item.url) || image.path : image.path,
      visibility: taskVisibility,
      width,
      height,
      resolution: item.resolution || (width && height ? `${width}x${height}` : image.resolution),
      outputFormat: item.output_format || task.output_format || image.outputFormat,
      revised_prompt: item.revised_prompt,
      text_response: undefined,
      error: undefined,
    } satisfies Partial<StoredImage>;
  };

  if (task.status === "success") {
    const item = task.data?.[dataIndex];
    if (!item?.b64_json && !item?.url) {
      return updateStoredImage(image, {
        taskId: task.id,
        taskStatus: "success",
        status: "error",
        error: `未返回第 ${dataIndex + 1} 张图片数据`,
      });
    }
    return updateStoredImage(image, successUpdates(item));
  }

  if (task.status === "queued" || task.status === "running") {
    return updateStoredImage(image, {
      taskId: task.id,
      taskStatus: creationTaskImageStatus(task, dataIndex) || (task.status === "queued" ? "queued" : "running"),
      status: "loading",
      text_response: undefined,
      error: undefined,
    });
  }

  if (task.status === "cancelled") {
    return updateStoredImage(image, {
      taskId: task.id,
      taskStatus: undefined,
      status: "cancelled",
      error: task.error || "任务已终止",
    });
  }

  return updateStoredImage(image, {
    taskId: task.id,
    taskStatus: undefined,
    status: "error",
    text_response: undefined,
    error: formatCreationTaskErrorMessage(task.error || "生成失败"),
  });
}

function imageDataIndexForTask(images: StoredImage[], imageIndex: number) {
  const taskId = images[imageIndex]?.taskId || images[imageIndex]?.id;
  if (!taskId) {
    return 0;
  }
  return images.slice(0, imageIndex + 1).filter((image) => (image.taskId || image.id) === taskId).length - 1;
}

function deriveTurnStatus(images: StoredImage[]): ImageTurnStatus {
  if (images.some((image) => image.status === "loading" && image.taskStatus === "running")) {
    return "generating";
  }
  if (images.some((image) => image.status === "loading")) {
    return "queued";
  }
  if (images.some((image) => image.status === "success")) {
    return "success";
  }
  if (images.some((image) => image.status === "cancelled")) {
    return "cancelled";
  }
  return "error";
}

function isActiveCreationTask(task: CreationTask) {
  return task.status === "queued" || task.status === "running";
}

function applyTaskToConversation(conversation: ImageConversation, task: CreationTask): ImageConversation {
  const now = new Date().toISOString();
  return {
    ...conversation,
    updatedAt: now,
    turns: conversation.turns.map((turn) => {
      const images = turn.images.map((image, imageIndex) => {
        if (image.taskId !== task.id) {
          return image;
        }
        return taskDataToStoredImage(image, task, imageDataIndexForTask(turn.images, imageIndex), turn.visibility);
      });
      const status = deriveTurnStatus(images);
      return {
        ...turn,
        images,
        status,
        processingStartedAt: turn.processingStartedAt || now,
        error: status === "error" ? images.find((image) => image.status === "error")?.error : undefined,
      };
    }),
  };
}

function markConversationError(conversation: ImageConversation, message: string): ImageConversation {
  const now = new Date().toISOString();
  return {
    ...conversation,
    updatedAt: now,
    turns: conversation.turns.map((turn) => ({
      ...turn,
      status: "error",
      error: message,
      images: turn.images.map((image) => ({
        ...image,
        status: "error",
        taskStatus: undefined,
        error: message,
      })),
    })),
  };
}

function buildConversation(args: {
  prompt: string;
  markedDataUrl: string;
  markedFile: File;
  taskId: string;
  model: ImageModel;
  count: number;
  visibility: ImageVisibility;
  outputFormat: ImageOutputFormat;
}): ImageConversation {
  const now = new Date().toISOString();
  const turnId = createClientId("retouch-turn");
  const images: StoredImage[] = Array.from({ length: args.count }, (_, index) => ({
    id: `${args.taskId}-${index + 1}`,
    taskId: args.taskId,
    taskStatus: "queued",
    status: "loading",
    visibility: args.visibility,
  }));
  const turn: ImageTurn = {
    id: turnId,
    prompt: args.prompt,
    model: args.model,
    mode: "image",
    referenceImages: [
      {
        name: args.markedFile.name,
        type: args.markedFile.type || "image/png",
        dataUrl: args.markedDataUrl,
        source: "upload",
      },
    ],
    count: args.count,
    size: "",
    outputFormat: args.outputFormat,
    visibility: args.visibility,
    images,
    createdAt: now,
    processingStartedAt: now,
    status: "queued",
  };
  return {
    id: createClientId("retouch-conversation"),
    title: `局部修图：${args.prompt.slice(0, 28) || "未命名"}`,
    createdAt: now,
    updatedAt: now,
    turns: [turn],
  };
}

function resultImageSrc(image: StoredImage) {
  if (image.b64_json) {
    return `data:image/png;base64,${image.b64_json}`;
  }
  return image.url || "";
}


function imageExtension(outputFormat?: string) {
  if (outputFormat === "jpeg") {
    return "jpg";
  }
  return outputFormat || "png";
}

function retouchImageFileName(turn: ImageTurn, index: number, outputFormat?: string) {
  const date = new Date(turn.createdAt);
  const safeIndex = String(index + 1).padStart(2, "0");
  const extension = imageExtension(outputFormat);
  if (Number.isNaN(date.getTime())) {
    return `retouch-${turn.id.slice(0, 8)}-${safeIndex}.${extension}`;
  }
  const yyyy = String(date.getFullYear());
  const mm = String(date.getMonth() + 1).padStart(2, "0");
  const dd = String(date.getDate()).padStart(2, "0");
  const hh = String(date.getHours()).padStart(2, "0");
  const min = String(date.getMinutes()).padStart(2, "0");
  const sec = String(date.getSeconds()).padStart(2, "0");
  return `retouch-${yyyy}${mm}${dd}-${hh}${min}${sec}-${safeIndex}.${extension}`;
}

function buildLightboxImages(turn: ImageTurn): RetouchLightboxImage[] {
  return turn.images.flatMap((image, index) => {
    const src = resultImageSrc(image);
    if (!src) {
      return [];
    }
    return [{
      id: image.id,
      src,
      fileName: retouchImageFileName(turn, index, image.outputFormat || turn.outputFormat),
      outputFormat: image.outputFormat || turn.outputFormat,
      dimensions: image.width && image.height ? `${image.width} x ${image.height}` : undefined,
    }];
  });
}

async function downloadRetouchImage(image: StoredImage, turn: ImageTurn, index: number) {
  const src = resultImageSrc(image);
  if (!src) {
    throw new Error("未找到可保存的图片数据");
  }
  let href = src;
  let objectUrl = "";
  if (!src.startsWith("data:")) {
    const blob = shouldUseAuthenticatedImageFallback(src)
      ? await fetchAuthenticatedImageBlob(src)
      : await fetch(src).then((response) => {
          if (!response.ok) {
            throw new Error("下载图片失败");
          }
          return response.blob();
        });
    objectUrl = URL.createObjectURL(blob);
    href = objectUrl;
  }
  const link = document.createElement("a");
  link.href = href;
  link.download = retouchImageFileName(turn, index, image.outputFormat || turn.outputFormat);
  document.body.appendChild(link);
  link.click();
  link.remove();
  if (objectUrl) {
    window.setTimeout(() => URL.revokeObjectURL(objectUrl), 1000);
  }
}

async function resultImageToFile(image: StoredImage, turn: ImageTurn, index: number) {
  const fileName = retouchImageFileName(turn, index, image.outputFormat || turn.outputFormat);
  if (image.b64_json) {
    const blob = dataUrlToBlob(resultImageSrc(image));
    return new File([blob], fileName, { type: blob.type || "image/png" });
  }
  if (!image.url) {
    throw new Error("未找到可继续编辑的图片数据");
  }
  const blob = shouldUseAuthenticatedImageFallback(image.url)
    ? await fetchAuthenticatedImageBlob(image.url)
    : await fetch(image.url).then((response) => {
        if (!response.ok) {
          throw new Error("读取结果图片失败");
        }
        return response.blob();
      });
  return new File([blob], fileName, { type: blob.type || "image/png" });
}
function RetouchPageContent() {
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const canvasRef = useRef<RetouchCanvasHandle | null>(null);
  const [sourceFile, setSourceFile] = useState<File | null>(null);
  const [sourceUrl, setSourceUrl] = useState("");
  const [userPrompt, setUserPrompt] = useState("");
  const [brushSize, setBrushSize] = useState(18);
  const [markerColor, setMarkerColor] = useState<RetouchMarkerColor>(DEFAULT_MARKER);
  const [hasMarks, setHasMarks] = useState(false);
  const [model, setModel] = useState<ImageModel>(DEFAULT_IMAGE_MODEL);
  const [count, setCount] = useState("1");
  const [visibility, setVisibility] = useState<ImageVisibility>("private");
  const [submitState, setSubmitState] = useState<SubmitState>({ conversation: null, status: "idle" });
  const [lightboxImages, setLightboxImages] = useState<RetouchLightboxImage[]>([]);
  const [lightboxOpen, setLightboxOpen] = useState(false);
  const [lightboxIndex, setLightboxIndex] = useState(0);

  useEffect(() => {
    return () => {
      if (sourceUrl) {
        URL.revokeObjectURL(sourceUrl);
      }
    };
  }, [sourceUrl]);

  const finalPrompt = useMemo(() => buildRetouchPrompt(userPrompt, markerColor.name), [markerColor.name, userPrompt]);
  const isSubmitting = submitState.status === "submitting" || submitState.status === "polling";
  const resultTurns = [...(submitState.conversation?.turns || [])].reverse();

  const handleFileChange = async (file: File | undefined) => {
    if (!file) {
      return;
    }
    if (!file.type.startsWith("image/")) {
      toast.error("请选择图片文件");
      return;
    }
    if (sourceUrl) {
      URL.revokeObjectURL(sourceUrl);
    }
    setSourceFile(file);
    setSourceUrl(URL.createObjectURL(file));
    setHasMarks(false);
    setSubmitState({ conversation: null, status: "idle" });
  };

  const pollTask = useCallback(async (conversation: ImageConversation, taskId: string) => {
    let current = conversation;
    for (let attempt = 0; attempt < MAX_POLL_ATTEMPTS; attempt += 1) {
      await sleep(POLL_INTERVAL_MS);
      const taskList = await fetchCreationTasks([taskId]);
      const task = taskList.items.find((item) => item.id === taskId);
      if (!task) {
        continue;
      }
      current = applyTaskToConversation(current, task);
      await saveImageConversation(current);
      setSubmitState({ conversation: current, status: isActiveCreationTask(task) ? "polling" : "done" });
      setImageTurnProgress(current.id, current.turns[0].id, {
        message: task.status === "running" ? "正在局部修图" : task.status === "queued" ? "任务排队中" : "局部修图完成",
        detail: isActiveCreationTask(task) ? "结果会自动同步到图片任务队列" : "可在创作台或图片库继续查看",
      });
      if (!isActiveCreationTask(task)) {
        clearImageTurnProgress(current.id, current.turns[0].id);
        return current;
      }
    }
    throw new Error("图片任务等待超时，请稍后到任务队列查看结果");
  }, []);

  const handleSubmit = async () => {
    if (!sourceFile || !sourceUrl) {
      toast.error("请先上传原图");
      return;
    }
    if (!hasMarks) {
      toast.error("请先标注需要修改的区域");
      return;
    }
    if (!userPrompt.trim()) {
      toast.error("请输入想要修改的内容");
      return;
    }

    setSubmitState((current) => ({ ...current, status: "submitting" }));
    let conversation: ImageConversation | null = null;
    try {
      const markedFile = await canvasRef.current?.exportMarkedImage();
      if (!markedFile) {
        throw new Error("导出标注图失败");
      }
      const markedDataUrl = await fileToDataUrl(markedFile);
      const taskId = createClientId("retouch-task");
      const outputFormat = IMAGE_OUTPUT_FORMAT_OPTIONS.some((option) => option.value === RETOUCH_OUTPUT_FORMAT) && isImageOutputFormat(RETOUCH_OUTPUT_FORMAT)
        ? RETOUCH_OUTPUT_FORMAT
        : undefined;
      const requestedCount = Math.max(1, Math.min(4, Number(count) || 1));
      const nextConversation = buildConversation({
        prompt: finalPrompt,
        markedDataUrl,
        markedFile,
        taskId,
        model,
        count: requestedCount,
        visibility,
        outputFormat: outputFormat || "png",
      });
      const nextTurn = nextConversation.turns[0];
      conversation = submitState.conversation
        ? {
            ...submitState.conversation,
            updatedAt: nextConversation.updatedAt,
            turns: [...submitState.conversation.turns, nextTurn],
          }
        : nextConversation;
      await saveImageConversation(conversation);
      setSubmitState({ conversation, status: "submitting" });
      setImageTurnProgress(conversation.id, nextTurn.id, {
        message: "正在提交局部修图任务",
        detail: "已生成标注图并准备上传",
      });

      const task = await createImageEditTask(
        taskId,
        markedFile,
        finalPrompt,
        model,
        undefined,
        undefined,
        requestedCount,
        undefined,
        visibility,
        undefined,
        outputFormat,
      );
      conversation = applyTaskToConversation(conversation, task);
      await saveImageConversation(conversation);
      setSubmitState({ conversation, status: isActiveCreationTask(task) ? "polling" : "done" });
      toast.success("已提交局部修图任务");
      if (isActiveCreationTask(task)) {
        await pollTask(conversation, taskId);
      } else {
        clearImageTurnProgress(conversation.id, nextTurn.id);
      }
    } catch (error) {
      const message = formatCreationTaskError(error, "提交局部修图失败");
      if (conversation) {
        const failed = markConversationError(conversation, message);
        await saveImageConversation(failed);
        clearImageTurnProgress(failed.id, failed.turns[failed.turns.length - 1]?.id || "");
        setSubmitState({ conversation: failed, status: "error" });
      } else {
        setSubmitState((current) => ({ ...current, status: "error" }));
      }
      toast.error(message);
    }
  };


  const openLightbox = (images: RetouchLightboxImage[], index: number) => {
    if (images.length === 0) {
      return;
    }
    setLightboxImages(images);
    setLightboxIndex(Math.max(0, Math.min(index, images.length - 1)));
    setLightboxOpen(true);
  };

  const handleDownload = async (image: StoredImage, turn: ImageTurn, index: number) => {
    try {
      await downloadRetouchImage(image, turn, index);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存图片失败");
    }
  };

  const handleContinueWithResult = async (image: StoredImage, turn: ImageTurn, index: number) => {
    try {
      const file = await resultImageToFile(image, turn, index);
      if (sourceUrl.startsWith("blob:")) {
        URL.revokeObjectURL(sourceUrl);
      }
      setSourceFile(file);
      setSourceUrl(URL.createObjectURL(file));
      setUserPrompt("");
      setHasMarks(false);
      window.scrollTo({ top: 0, behavior: "smooth" });
      toast.success("已载入结果图，可继续标注修改");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "载入结果图失败");
    }
  };
  return (
    <div className="min-h-[calc(100vh-72px)] bg-[#f5f7fa] px-4 py-5 text-slate-950 sm:px-6 lg:px-8">
      <div className="mx-auto grid max-w-[1560px] gap-5">
        <section className="min-w-0 rounded-[8px] border border-slate-200 bg-white p-4 shadow-sm sm:p-5">
          <div className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 pb-4">
            <div>
              <h1 className="text-2xl font-semibold tracking-normal text-slate-950">局部修图</h1>
              <p className="mt-1 text-sm text-slate-500">上传原图，圈出要改的位置，剩下的交给图片编辑任务。</p>
            </div>
            <Button type="button" variant="outline" onClick={() => fileInputRef.current?.click()} disabled={isSubmitting}>
              <Upload className="size-4" />
              上传图片
            </Button>
            <input
              ref={fileInputRef}
              type="file"
              accept="image/*"
              className="hidden"
              onChange={(event) => {
                void handleFileChange(event.target.files?.[0]);
                event.currentTarget.value = "";
              }}
            />
          </div>

          {sourceUrl ? (
            <RetouchCanvas
              ref={canvasRef}
              imageFile={sourceFile}
              imageUrl={sourceUrl}
              brushSize={brushSize}
              onMarkerChange={(color, marks) => {
                setMarkerColor(color);
                setHasMarks(marks);
              }}
            />
          ) : (
            <button
              type="button"
              onClick={() => fileInputRef.current?.click()}
              className="flex min-h-[520px] w-full flex-col items-center justify-center rounded-[8px] border border-dashed border-slate-300 bg-slate-50 text-center transition hover:border-[#1456f0]/50 hover:bg-[#eef4ff]"
            >
              <span className="flex size-16 items-center justify-center rounded-full bg-white text-[#1456f0] shadow-sm ring-1 ring-slate-200">
                <ImagePlus className="size-8" />
              </span>
              <span className="mt-4 text-base font-medium text-slate-900">选择一张需要 P 图的原图</span>
              <span className="mt-1 text-sm text-slate-500">支持 PNG、JPEG、WebP 等浏览器可读取格式</span>
            </button>
          )}
        </section>

        <aside className="grid content-start gap-4 xl:grid-cols-[minmax(280px,360px)_minmax(280px,360px)_minmax(0,1fr)]">
          <Card className="rounded-[8px] bg-white">
            <CardHeader className="p-5 pb-3">
              <CardTitle className="flex items-center gap-2 text-base">
                <Brush className="size-4 text-[#1456f0]" />
                标注与提示词
              </CardTitle>
              <CardDescription>红色可见时优先用红色；背景接近红色时自动切换高对比颜色。</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-4 p-5 pt-0">
              <Field>
                <FieldLabel htmlFor="retouch-brush-size">笔刷大小</FieldLabel>
                <div className="flex items-center gap-3">
                  <Input
                    id="retouch-brush-size"
                    type="range"
                    min={8}
                    max={48}
                    value={brushSize}
                    onChange={(event) => setBrushSize(Number(event.target.value) || 18)}
                    className="px-0"
                  />
                  <span className="w-10 text-right text-sm tabular-nums text-slate-600">{brushSize}</span>
                </div>
              </Field>

              <Field>
                <FieldLabel>标注颜色</FieldLabel>
                <div className="flex items-center gap-2 rounded-[8px] border border-slate-200 bg-slate-50 px-3 py-2 text-sm text-slate-700">
                  <span className="size-4 rounded-full ring-2 ring-white shadow" style={{ backgroundColor: markerColor.css }} />
                  {markerColor.name}
                  <span className="ml-auto text-xs text-slate-500">{hasMarks ? "已标注" : "等待标注"}</span>
                </div>
              </Field>

              <Field>
                <FieldLabel htmlFor="retouch-prompt">修改要求</FieldLabel>
                <Textarea
                  id="retouch-prompt"
                  value={userPrompt}
                  onChange={(event) => setUserPrompt(event.target.value)}
                  placeholder="例如：把杯子换成一束白色玫瑰，保持桌面光影一致"
                  className="min-h-36 resize-none"
                />
                <FieldDescription>只写你想改成什么，区域约束和一致性要求会自动补全。</FieldDescription>
              </Field>
            </CardContent>
          </Card>

          <Card className="rounded-[8px] bg-white">
            <CardHeader className="p-5 pb-3">
              <CardTitle className="flex items-center gap-2 text-base">
                <ShieldCheck className="size-4 text-emerald-600" />
                提交设置
              </CardTitle>
            </CardHeader>
            <CardContent className="grid gap-4 p-5 pt-0">
              <div className="grid grid-cols-2 gap-3">
                <Field>
                  <FieldLabel>模型</FieldLabel>
                  <Select value={model} onValueChange={(value) => setModel(value as ImageModel)} disabled={isSubmitting}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {IMAGE_CREATION_MODEL_OPTIONS.map((option) => (
                        <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
                <Field>
                  <FieldLabel>数量</FieldLabel>
                  <Select value={count} onValueChange={setCount} disabled={isSubmitting}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {[1, 2, 3, 4].map((value) => (
                        <SelectItem key={value} value={String(value)}>{value} 张</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
              </div>

              <Field>
                <FieldLabel>可见性</FieldLabel>
                <Select value={visibility} onValueChange={(value) => setVisibility(value as ImageVisibility)} disabled={isSubmitting}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="private">私有</SelectItem>
                    <SelectItem value="public">公开</SelectItem>
                  </SelectContent>
                </Select>
              </Field>

              <Button
                type="button"
                className="h-11 bg-[#1456f0] text-white hover:bg-[#0f46c7]"
                onClick={() => void handleSubmit()}
                disabled={isSubmitting || !sourceFile || !hasMarks || !userPrompt.trim()}
              >
                {isSubmitting ? <LoaderCircle className="size-4 animate-spin" /> : <Send className="size-4" />}
                提交局部修图
              </Button>
            </CardContent>
          </Card>

          <Card className="rounded-[8px] bg-white">
            <CardHeader className="p-5 pb-3">
              <CardTitle className="flex items-center gap-2 text-base">
                <Sparkles className="size-4 text-amber-500" />
                最近结果
              </CardTitle>
            </CardHeader>
            <CardContent className="p-5 pt-0">
              {resultTurns.length > 0 ? (
                <div className="grid gap-4">
                  {resultTurns.map((turn, turnIndex) => {
                    const lightboxItems = buildLightboxImages(turn);
                    return (
                      <article key={turn.id} className="grid gap-3 rounded-[8px] border border-slate-200 bg-slate-50 p-3">
                        <div className="flex flex-wrap items-start justify-between gap-2">
                          <div className="min-w-0">
                            <div className="flex items-center gap-2">
                              <span className="text-sm font-semibold text-slate-900">第 {resultTurns.length - turnIndex} 轮</span>
                              <span className={cn(
                                "rounded-full px-2 py-0.5 text-xs font-medium",
                                turn.status === "success" ? "bg-emerald-50 text-emerald-700" :
                                  turn.status === "error" ? "bg-rose-50 text-rose-700" : "bg-sky-50 text-[#1456f0]",
                              )}>
                                {turn.status === "success" ? "任务已完成" : turn.status === "error" ? "任务失败" : "任务处理中"}
                              </span>
                            </div>
                            <p className="mt-1 line-clamp-2 text-xs leading-5 text-slate-500">{turn.prompt}</p>
                          </div>
                          <span className="rounded-full bg-white px-2.5 py-1 text-xs text-slate-500 ring-1 ring-slate-200">
                            {turn.images.length} 张
                          </span>
                        </div>
                        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4">
                          {turn.images.map((image, index) => {
                            const src = resultImageSrc(image);
                            const currentLightboxIndex = lightboxItems.findIndex((item) => item.id === image.id);
                            return (
                              <div key={image.id} className="overflow-hidden rounded-[8px] border border-slate-200 bg-white shadow-sm">
                                {src ? (
                                  <button
                                    type="button"
                                    className="group relative block w-full bg-slate-100"
                                    onClick={() => openLightbox(lightboxItems, currentLightboxIndex >= 0 ? currentLightboxIndex : 0)}
                                  >
                                    <AuthenticatedImage src={src} alt={`局部修图结果 ${index + 1}`} className="aspect-[4/3] w-full object-contain" />
                                    <span className="absolute inset-0 flex items-center justify-center bg-black/0 opacity-0 transition group-hover:bg-black/20 group-hover:opacity-100">
                                      <span className="inline-flex items-center gap-2 rounded-full bg-white/95 px-3 py-1.5 text-xs font-medium text-slate-900 shadow-sm">
                                        <Eye className="size-4" />
                                        查看
                                      </span>
                                    </span>
                                  </button>
                                ) : (
                                  <div className="flex aspect-[4/3] items-center justify-center bg-slate-100 text-xs text-slate-500">
                                    {image.status === "error" ? image.error || "失败" : "等待结果"}
                                  </div>
                                )}
                                <div className="flex flex-wrap items-center justify-between gap-2 px-3 py-3">
                                  <span className="text-xs text-slate-500">
                                    {image.width && image.height ? `${image.width} x ${image.height}` : image.status === "success" ? "已保存到图库" : "处理中"}
                                  </span>
                                  <div className="flex items-center gap-1.5">
                                    <Button type="button" variant="outline" size="sm" disabled={!src} onClick={() => void handleDownload(image, turn, index)}>
                                      <Download className="size-4" />
                                      保存
                                    </Button>
                                    <Button type="button" variant="outline" size="sm" disabled={!src} onClick={() => void handleContinueWithResult(image, turn, index)}>
                                      <MessageSquarePlus className="size-4" />
                                      继续
                                    </Button>
                                  </div>
                                </div>
                              </div>
                            );
                          })}
                        </div>
                      </article>
                    );
                  })}
                </div>
              ) : (
                <p className="rounded-[8px] border border-dashed border-slate-200 bg-slate-50 px-3 py-10 text-center text-sm text-slate-500">
                  提交后会像创作台一样按轮次显示结果，可查看、保存，也可继续编辑。
                </p>
              )}
            </CardContent>
          </Card>
        </aside>
      </div>

      <ImageLightbox
        images={lightboxImages}
        currentIndex={lightboxIndex}
        open={lightboxOpen}
        onOpenChange={setLightboxOpen}
        onIndexChange={setLightboxIndex}
      />
    </div>
  );
}

export default function RetouchPage() {
  const { isCheckingAuth, session } = useAuthGuard(undefined, "/image");

  if (isCheckingAuth || !session) {
    return (
      <div className="flex min-h-[40vh] items-center justify-center">
        <LoaderCircle className="size-5 animate-spin text-slate-400" />
      </div>
    );
  }

  return <RetouchPageContent />;
}




