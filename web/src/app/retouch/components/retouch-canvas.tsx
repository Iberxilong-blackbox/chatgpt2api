"use client";

import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import { RotateCcw, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { RetouchMarkerColor } from "@/app/retouch/retouch-prompt";

type Point = {
  x: number;
  y: number;
};

type Stroke = {
  points: Point[];
  color: MarkerCandidate;
  width: number;
};

type MarkerCandidate = RetouchMarkerColor & {
  rgb: [number, number, number];
};

export type RetouchCanvasHandle = {
  exportMarkedImage: () => Promise<File>;
  exportMaskDataUrl: () => string;
};

type RetouchCanvasProps = {
  imageFile: File | null;
  imageUrl: string;
  brushSize: number;
  className?: string;
  onMarkerChange?: (color: RetouchMarkerColor, hasMarks: boolean) => void;
  onBrushSizeChange?: (size: number) => void;
};

const BRUSH_PRESETS = [
  { size: 2, label: "S" },
  { size: 5, label: "M" },
  { size: 8, label: "L" },
  { size: 10, label: "XL" },
] as const;

const MARKER_COLORS: MarkerCandidate[] = [
  { id: "red", name: "红色", css: "#ff1f1f", rgb: [255, 31, 31] },
  { id: "cyan", name: "青色", css: "#00d5ff", rgb: [0, 213, 255] },
  { id: "yellow", name: "黄色", css: "#ffd400", rgb: [255, 212, 0] },
  { id: "magenta", name: "品红色", css: "#ff35d1", rgb: [255, 53, 209] },
  { id: "blue", name: "蓝色", css: "#2b6cff", rgb: [43, 108, 255] },
  { id: "green", name: "绿色", css: "#1fbf62", rgb: [31, 191, 98] },
  { id: "white", name: "白色", css: "#ffffff", rgb: [255, 255, 255] },
  { id: "black", name: "黑色", css: "#050505", rgb: [5, 5, 5] },
];

const RED_MARKER = MARKER_COLORS[0];
const DEFAULT_MARKER = RED_MARKER;
const MIN_RED_CONTRAST = 3.2;

function luminance([red, green, blue]: [number, number, number]) {
  const convert = (value: number) => {
    const channel = value / 255;
    return channel <= 0.03928 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * convert(red) + 0.7152 * convert(green) + 0.0722 * convert(blue);
}

function contrastRatio(a: [number, number, number], b: [number, number, number]) {
  const first = luminance(a);
  const second = luminance(b);
  const lighter = Math.max(first, second);
  const darker = Math.min(first, second);
  return (lighter + 0.05) / (darker + 0.05);
}

function averageSampleColor(context: CanvasRenderingContext2D, points: Point[], width: number, height: number): [number, number, number] {
  if (points.length === 0) {
    return [255, 255, 255];
  }

  const samplePoints = points.filter((_, index) => index % Math.max(1, Math.floor(points.length / 24)) === 0).slice(0, 24);
  let red = 0;
  let green = 0;
  let blue = 0;
  let count = 0;

  for (const point of samplePoints) {
    const x = Math.max(0, Math.min(width - 1, Math.round(point.x)));
    const y = Math.max(0, Math.min(height - 1, Math.round(point.y)));
    const data = context.getImageData(x, y, 1, 1).data;
    red += data[0] || 0;
    green += data[1] || 0;
    blue += data[2] || 0;
    count += 1;
  }

  if (count === 0) {
    return [255, 255, 255];
  }

  return [Math.round(red / count), Math.round(green / count), Math.round(blue / count)];
}

function pickMarkerColor(background: [number, number, number]) {
  if (contrastRatio(RED_MARKER.rgb, background) >= MIN_RED_CONTRAST) {
    return RED_MARKER;
  }

  return MARKER_COLORS.slice(1).reduce((best, candidate) =>
    contrastRatio(candidate.rgb, background) > contrastRatio(best.rgb, background) ? candidate : best,
  );
}

function outerStrokeColor(background: [number, number, number]) {
  return contrastRatio([0, 0, 0], background) >= contrastRatio([255, 255, 255], background) ? "#050505" : "#ffffff";
}

function drawStroke(context: CanvasRenderingContext2D, stroke: Stroke, outlineColor: string) {
  if (stroke.points.length === 0) {
    return;
  }

  const drawLine = (color: string, width: number) => {
    context.save();
    context.lineCap = "round";
    context.lineJoin = "round";
    context.strokeStyle = color;
    context.lineWidth = width;
    context.beginPath();
    stroke.points.forEach((point, index) => {
      if (index === 0) {
        context.moveTo(point.x, point.y);
      } else {
        context.lineTo(point.x, point.y);
      }
    });
    if (stroke.points.length === 1) {
      const point = stroke.points[0];
      context.lineTo(point.x + 0.1, point.y + 0.1);
    }
    context.stroke();
    context.restore();
  };

  drawLine(outlineColor, stroke.width + 8);
  drawLine(stroke.color.css, stroke.width);
}

function drawMaskStroke(context: CanvasRenderingContext2D, stroke: Stroke) {
  if (stroke.points.length === 0) {
    return;
  }

  context.save();
  context.lineCap = "round";
  context.lineJoin = "round";
  context.strokeStyle = "#ffffff";
  context.lineWidth = stroke.width + 12;
  context.beginPath();
  stroke.points.forEach((point, index) => {
    if (index === 0) {
      context.moveTo(point.x, point.y);
    } else {
      context.lineTo(point.x, point.y);
    }
  });
  if (stroke.points.length === 1) {
    const point = stroke.points[0];
    context.lineTo(point.x + 0.1, point.y + 0.1);
  }
  context.stroke();
  context.restore();
}

function canvasToFile(canvas: HTMLCanvasElement, fileName: string) {
  return new Promise<File>((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (!blob) {
        reject(new Error("导出标注图失败"));
        return;
      }
      resolve(new File([blob], fileName, { type: "image/png" }));
    }, "image/png");
  });
}

export const RetouchCanvas = forwardRef<RetouchCanvasHandle, RetouchCanvasProps>(function RetouchCanvas(
  { imageFile, imageUrl, brushSize, className, onMarkerChange, onBrushSizeChange },
  ref,
) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const imageRef = useRef<HTMLImageElement | null>(null);
  const [imageSize, setImageSize] = useState({ width: 0, height: 0 });
  const [strokes, setStrokes] = useState<Stroke[]>([]);
  const [activePoints, setActivePoints] = useState<Point[]>([]);
  const [markerColor, setMarkerColor] = useState<MarkerCandidate>(DEFAULT_MARKER);
  const [outlineColor, setOutlineColor] = useState("#ffffff");
  const activePointerId = useRef<number | null>(null);

  const hasMarks = strokes.length > 0 || activePoints.length > 0;

  const redraw = useCallback(() => {
    const canvas = canvasRef.current;
    const image = imageRef.current;
    if (!canvas || !image || !imageSize.width || !imageSize.height) {
      return;
    }

    const context = canvas.getContext("2d", { willReadFrequently: true });
    if (!context) {
      return;
    }

    context.clearRect(0, 0, canvas.width, canvas.height);
    context.drawImage(image, 0, 0, canvas.width, canvas.height);
    for (const stroke of strokes) {
      const background = averageSampleColor(context, stroke.points, canvas.width, canvas.height);
      drawStroke(context, stroke, outerStrokeColor(background));
    }
    if (activePoints.length > 0) {
      drawStroke(context, { points: activePoints, color: markerColor, width: brushSize }, outlineColor);
    }
  }, [activePoints, brushSize, imageSize.height, imageSize.width, markerColor, outlineColor, strokes]);

  useEffect(() => {
    const image = new Image();
    image.onload = () => {
      imageRef.current = image;
      setImageSize({ width: image.naturalWidth, height: image.naturalHeight });
      setStrokes([]);
      setActivePoints([]);
      setMarkerColor(DEFAULT_MARKER);
      setOutlineColor("#ffffff");
    };
    image.src = imageUrl;
  }, [imageUrl]);

  useEffect(() => {
    redraw();
  }, [redraw]);

  useEffect(() => {
    onMarkerChange?.(markerColor, strokes.length > 0);
  }, [markerColor, onMarkerChange, strokes.length]);

  const viewBox = useMemo(() => {
    if (!imageSize.width || !imageSize.height) {
      return undefined;
    }
    return { aspectRatio: `${imageSize.width} / ${imageSize.height}` };
  }, [imageSize.height, imageSize.width]);

  const eventPoint = useCallback((event: React.PointerEvent<HTMLCanvasElement>): Point | null => {
    const canvas = canvasRef.current;
    if (!canvas || !imageSize.width || !imageSize.height) {
      return null;
    }
    const rect = canvas.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) {
      return null;
    }
    return {
      x: ((event.clientX - rect.left) / rect.width) * canvas.width,
      y: ((event.clientY - rect.top) / rect.height) * canvas.height,
    };
  }, [imageSize.height, imageSize.width]);

  const updateActiveColor = useCallback((points: Point[]) => {
    const canvas = canvasRef.current;
    const image = imageRef.current;
    if (!canvas || !image) {
      return;
    }
    const context = canvas.getContext("2d", { willReadFrequently: true });
    if (!context) {
      return;
    }
    context.clearRect(0, 0, canvas.width, canvas.height);
    context.drawImage(image, 0, 0, canvas.width, canvas.height);
    const background = averageSampleColor(context, points, canvas.width, canvas.height);
    setMarkerColor(pickMarkerColor(background));
    setOutlineColor(outerStrokeColor(background));
  }, []);

  const handlePointerDown = (event: React.PointerEvent<HTMLCanvasElement>) => {
    const point = eventPoint(event);
    if (!point) {
      return;
    }
    event.currentTarget.setPointerCapture(event.pointerId);
    activePointerId.current = event.pointerId;
    setActivePoints([point]);
    updateActiveColor([point]);
  };

  const handlePointerMove = (event: React.PointerEvent<HTMLCanvasElement>) => {
    if (activePointerId.current !== event.pointerId) {
      return;
    }
    const point = eventPoint(event);
    if (!point) {
      return;
    }
    setActivePoints((current) => {
      const last = current[current.length - 1];
      if (last && Math.hypot(last.x - point.x, last.y - point.y) < 2) {
        return current;
      }
      const next = [...current, point];
      updateActiveColor(next);
      return next;
    });
  };

  const finishStroke = (event: React.PointerEvent<HTMLCanvasElement>) => {
    if (activePointerId.current !== event.pointerId) {
      return;
    }
    try {
      event.currentTarget.releasePointerCapture(event.pointerId);
    } catch {
      // Pointer capture may already be released by the browser.
    }
    activePointerId.current = null;
    setActivePoints((current) => {
      if (current.length === 0) {
        return [];
      }
      setStrokes((items) => [...items, { points: current, color: markerColor, width: brushSize }]);
      return [];
    });
  };

  useImperativeHandle(ref, () => ({
    async exportMarkedImage() {
      const canvas = canvasRef.current;
      if (!canvas || !imageRef.current || strokes.length === 0) {
        throw new Error("请先标注需要修改的区域");
      }
      redraw();
      const baseName = imageFile?.name.replace(/\.[^.]+$/, "") || "retouch-source";
      return canvasToFile(canvas, `${baseName}-marked.png`);
    },
    exportMaskDataUrl() {
      const canvas = canvasRef.current;
      if (!canvas || !imageRef.current || strokes.length === 0) {
        throw new Error("请先标注需要修改的区域");
      }

      const maskCanvas = document.createElement("canvas");
      maskCanvas.width = canvas.width;
      maskCanvas.height = canvas.height;
      const context = maskCanvas.getContext("2d");
      if (!context) {
        throw new Error("导出 mask 失败");
      }

      context.fillStyle = "#000000";
      context.fillRect(0, 0, maskCanvas.width, maskCanvas.height);
      strokes.forEach((stroke) => drawMaskStroke(context, stroke));
      return maskCanvas.toDataURL("image/png");
    },
  }), [imageFile?.name, redraw, strokes]);

  const handleUndo = () => {
    setStrokes((items) => items.slice(0, -1));
  };

  const handleClear = () => {
    setStrokes([]);
    setActivePoints([]);
  };

  return (
    <div className={cn("flex min-h-0 flex-1 flex-col gap-3", className)}>
      <div className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden rounded-[8px] border border-slate-200 bg-[linear-gradient(45deg,#f8fafc_25%,transparent_25%),linear-gradient(-45deg,#f8fafc_25%,transparent_25%),linear-gradient(45deg,transparent_75%,#f8fafc_75%),linear-gradient(-45deg,transparent_75%,#f8fafc_75%)] bg-[length:24px_24px] bg-[position:0_0,0_12px,12px_-12px,-12px_0] shadow-sm">
        <canvas
          ref={canvasRef}
          width={imageSize.width || 1}
          height={imageSize.height || 1}
          style={viewBox}
          className="block h-auto w-auto max-h-full max-w-full touch-none cursor-crosshair object-contain"
          onPointerDown={handlePointerDown}
          onPointerMove={handlePointerMove}
          onPointerUp={finishStroke}
          onPointerCancel={finishStroke}
        />
        {!imageSize.width ? (
          <div className="absolute inset-0 flex items-center justify-center text-sm text-slate-500">正在读取图片</div>
        ) : !hasMarks ? (
          <div className="pointer-events-none absolute inset-0 flex select-none items-center justify-center">
            <span className="rounded-full bg-slate-950/80 px-5 py-2.5 text-sm font-medium tracking-wide text-white/85 shadow-sm backdrop-blur-sm">
              框选或圈出需要修改的区域
            </span>
          </div>
        ) : null}
      </div>

      <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 rounded-[8px] border border-slate-200 bg-white px-3 py-2 shadow-sm">
        <div className="flex items-center gap-2 text-sm text-slate-600">
          <span className="size-4 rounded-full ring-2 ring-white shadow" style={{ backgroundColor: markerColor.css }} />
          <span>当前标注：{markerColor.name}</span>
          <span className="text-slate-400">{hasMarks ? `${strokes.length} 笔` : "未标注"}</span>
        </div>

        <div className="flex items-center gap-1.5">
          {BRUSH_PRESETS.map((preset) => (
            <button
              key={preset.size}
              type="button"
              onClick={() => onBrushSizeChange?.(preset.size)}
              className={[
                "flex size-9 items-center justify-center rounded-full text-xs font-medium transition",
                "hover:bg-slate-100",
                brushSize === preset.size
                  ? "bg-slate-950 text-white shadow-sm hover:bg-slate-800"
                  : "text-slate-500",
              ].join(" ")}
              title={`画笔粗细 ${preset.label} (${preset.size}px)`}
            >
              {preset.label}
            </button>
          ))}
        </div>

        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" size="sm" onClick={handleUndo} disabled={strokes.length === 0}>
            <RotateCcw className="size-4" />
            撤销
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={handleClear} disabled={!hasMarks}>
            <Trash2 className="size-4" />
            清空
          </Button>
        </div>
      </div>
    </div>
  );
});
