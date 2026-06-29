"use client";

import localforage from "localforage";

import { getStoredAuthSession, type StoredAuthSession } from "@/store/auth";
import type { ImageNode, ImageTreeAsset, ImageTreeSnapshot } from "@/store/useImageTreeStore";

export type RetouchHistorySession = ImageTreeSnapshot & {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  thumbnailUrl?: string;
  draftPrompt?: string;
};

export type RetouchHistoryStats = {
  imageCount: number;
  editCount: number;
};

const retouchHistoryStorage = localforage.createInstance({
  name: "chatgpt2api",
  storeName: "retouch_history",
});

export const RETOUCH_HISTORY_CHANGED_EVENT = "chatgpt2api:retouch-history-changed";
const RETOUCH_HISTORY_KEY_PREFIX = "items";
let retouchHistoryWriteQueue: Promise<void> = Promise.resolve();

function dispatchRetouchHistoryChanged() {
  if (typeof window === "undefined") {
    return;
  }
  window.dispatchEvent(new Event(RETOUCH_HISTORY_CHANGED_EVENT));
}

function historyScopeFromSession(session: StoredAuthSession | null) {
  if (!session) {
    return "anonymous";
  }
  const subjectId = session.subjectId.trim();
  if (!subjectId) {
    return `${session.provider || "local"}:${session.role}:unknown`;
  }
  return `${session.provider || "local"}:${session.role}:${subjectId}`;
}

async function retouchHistoryStorageKey() {
  const session = await getStoredAuthSession();
  return `${RETOUCH_HISTORY_KEY_PREFIX}:${historyScopeFromSession(session)}`;
}

function normalizeImageAsset(asset: ImageTreeAsset & Record<string, unknown>): ImageTreeAsset {
  const sequenceNumber = Number(asset.sequenceNumber);
  const width = Number(asset.width);
  const height = Number(asset.height);
  const source = asset.source === "upload" || asset.source === "generated" ? asset.source : undefined;
  return {
    id: String(asset.id || `${Date.now()}`),
    url: String(asset.url || ""),
    name: typeof asset.name === "string" ? asset.name : undefined,
    sequenceNumber: Number.isFinite(sequenceNumber) && sequenceNumber > 0 ? sequenceNumber : undefined,
    width: Number.isFinite(width) && width > 0 ? width : undefined,
    height: Number.isFinite(height) && height > 0 ? height : undefined,
    ...(source ? { source } : {}),
  };
}

function normalizeNode(node: ImageNode & Record<string, unknown>): ImageNode | null {
  if (!node || typeof node !== "object" || !node.baseImage || typeof node.baseImage !== "object") {
    return null;
  }
  const baseImage = normalizeImageAsset(node.baseImage as ImageTreeAsset & Record<string, unknown>);
  if (!baseImage.url) {
    return null;
  }
  const generatedImage = node.generatedImage && typeof node.generatedImage === "object"
    ? normalizeImageAsset(node.generatedImage as ImageTreeAsset & Record<string, unknown>)
    : undefined;
  return {
    id: String(node.id || `${Date.now()}`),
    parentId: typeof node.parentId === "string" ? node.parentId : null,
    baseImage,
    ...(generatedImage?.url ? { generatedImage } : {}),
    maskData: typeof node.maskData === "string" ? node.maskData : undefined,
    prompt: String(node.prompt || ""),
    childrenIds: Array.isArray(node.childrenIds) ? node.childrenIds.map(String) : [],
    createdAt: String(node.createdAt || new Date().toISOString()),
  };
}

function normalizeSession(session: RetouchHistorySession & Record<string, unknown>): RetouchHistorySession | null {
  const nodesById: Record<string, ImageNode> = {};
  const sourceNodes = session.nodesById && typeof session.nodesById === "object"
    ? session.nodesById as Record<string, ImageNode & Record<string, unknown>>
    : {};
  for (const [id, node] of Object.entries(sourceNodes)) {
    const normalized = normalizeNode(node);
    if (normalized) {
      nodesById[id] = normalized;
    }
  }

  const rootNodeId = typeof session.rootNodeId === "string" && nodesById[session.rootNodeId]
    ? session.rootNodeId
    : null;
  if (!rootNodeId) {
    return null;
  }

  const currentNodeId = typeof session.currentNodeId === "string" && nodesById[session.currentNodeId]
    ? session.currentNodeId
    : rootNodeId;
  const nextImageNumber = Number(session.nextImageNumber);

  return {
    id: String(session.id || `${Date.now()}`),
    title: String(session.title || "未命名修图"),
    createdAt: String(session.createdAt || new Date().toISOString()),
    updatedAt: String(session.updatedAt || session.createdAt || new Date().toISOString()),
    thumbnailUrl: typeof session.thumbnailUrl === "string" && session.thumbnailUrl ? session.thumbnailUrl : getRetouchSessionPreviewUrl({ nodesById, currentNodeId }),
    draftPrompt: typeof session.draftPrompt === "string" ? session.draftPrompt : "",
    nodesById,
    rootNodeId,
    currentNodeId,
    nextImageNumber: Number.isFinite(nextImageNumber) && nextImageNumber > 0 ? nextImageNumber : 1,
  };
}

function sortRetouchHistorySessions(sessions: RetouchHistorySession[]) {
  return [...sessions].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
}

function getTimestamp(value: string) {
  const time = new Date(value).getTime();
  return Number.isFinite(time) ? time : 0;
}

function pickLatestSession(current: RetouchHistorySession, next: RetouchHistorySession) {
  return getTimestamp(next.updatedAt) >= getTimestamp(current.updatedAt) ? next : current;
}

function queueRetouchHistoryWrite<T>(operation: () => Promise<T>): Promise<T> {
  const result = retouchHistoryWriteQueue.then(operation);
  retouchHistoryWriteQueue = result.then(
    () => undefined,
    () => undefined,
  );
  return result;
}

async function readStoredRetouchHistorySessions(storageKey?: string): Promise<RetouchHistorySession[]> {
  storageKey = storageKey || await retouchHistoryStorageKey();
  const items = (await retouchHistoryStorage.getItem<Array<RetouchHistorySession & Record<string, unknown>>>(storageKey)) || [];
  return items.map(normalizeSession).filter((item): item is RetouchHistorySession => Boolean(item));
}

export function getRetouchSessionPreviewUrl(session: Pick<RetouchHistorySession, "nodesById" | "currentNodeId">) {
  const currentNode = session.currentNodeId ? session.nodesById[session.currentNodeId] : null;
  const preview = currentNode?.generatedImage ?? currentNode?.baseImage;
  return preview?.url;
}

export function getRetouchHistoryStats(session: Pick<RetouchHistorySession, "nodesById">): RetouchHistoryStats {
  const images = new Set<string>();
  let editCount = 0;
  for (const node of Object.values(session.nodesById)) {
    images.add(node.baseImage.id);
    if (node.generatedImage) {
      images.add(node.generatedImage.id);
      editCount += 1;
    }
  }
  return { imageCount: images.size, editCount };
}

export async function listRetouchHistorySessions(): Promise<RetouchHistorySession[]> {
  return sortRetouchHistorySessions(await readStoredRetouchHistorySessions());
}

export async function saveRetouchHistorySession(session: RetouchHistorySession): Promise<void> {
  await queueRetouchHistoryWrite(async () => {
    const storageKey = await retouchHistoryStorageKey();
    const items = await readStoredRetouchHistorySessions(storageKey);
    const normalized = normalizeSession(session);
    if (!normalized) {
      return;
    }
    const current = items.find((item) => item.id === normalized.id);
    const persisted = current ? pickLatestSession(current, normalized) : normalized;
    await retouchHistoryStorage.setItem(
      storageKey,
      sortRetouchHistorySessions([
        persisted,
        ...items.filter((item) => item.id !== persisted.id),
      ]),
    );
    dispatchRetouchHistoryChanged();
  });
}

export async function deleteRetouchHistorySession(id: string): Promise<void> {
  await queueRetouchHistoryWrite(async () => {
    const storageKey = await retouchHistoryStorageKey();
    const items = await readStoredRetouchHistorySessions(storageKey);
    await retouchHistoryStorage.setItem(storageKey, items.filter((item) => item.id !== id));
    dispatchRetouchHistoryChanged();
  });
}

export async function clearRetouchHistorySessions(): Promise<void> {
  await queueRetouchHistoryWrite(async () => {
    await retouchHistoryStorage.removeItem(await retouchHistoryStorageKey());
    dispatchRetouchHistoryChanged();
  });
}