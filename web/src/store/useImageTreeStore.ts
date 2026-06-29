"use client";

import { create } from "zustand";

export type ImageTreeAsset = {
  id: string;
  url: string;
  name?: string;
  sequenceNumber?: number;
  width?: number;
  height?: number;
};

export type ImageNode = {
  id: string;
  parentId: string | null;
  baseImage: ImageTreeAsset;
  generatedImage?: ImageTreeAsset;
  maskData?: string;
  prompt: string;
  childrenIds: string[];
  createdAt: string;
};

export type AddRootNodePayload = {
  id?: string;
  baseImage: ImageTreeAsset;
  prompt?: string;
  maskData?: string;
};

export type AddNodePayload = {
  id?: string;
  parentId?: string | null;
  baseImage?: ImageTreeAsset;
  generatedImage: ImageTreeAsset;
  maskData?: string;
  prompt: string;
};

type ImageTreeState = {
  nodesById: Record<string, ImageNode>;
  rootNodeId: string | null;
  currentNodeId: string | null;
  nextImageNumber: number;
  addRootNode: (payload: AddRootNodePayload) => ImageNode;
  addNode: (payload: AddNodePayload) => ImageNode;
  navigateNode: (nodeId: string) => void;
  getCurrentNode: () => ImageNode | null;
  getAncestors: (nodeId?: string | null) => ImageNode[];
  resetTree: () => void;
};

function createNodeId(prefix = "image-node") {
  const random =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `${prefix}-${random}`;
}

export const useImageTreeStore = create<ImageTreeState>((set, get) => ({
  nodesById: {},
  rootNodeId: null,
  currentNodeId: null,
  nextImageNumber: 1,

  addRootNode: (payload) => {
    const state = get();
    const node: ImageNode = {
      id: payload.id ?? createNodeId("image-root"),
      parentId: null,
      baseImage: {
        ...payload.baseImage,
        sequenceNumber: payload.baseImage.sequenceNumber ?? state.nextImageNumber,
      },
      maskData: payload.maskData,
      prompt: payload.prompt?.trim() ?? "",
      childrenIds: [],
      createdAt: new Date().toISOString(),
    };

    set({
      nodesById: { [node.id]: node },
      rootNodeId: node.id,
      currentNodeId: node.id,
      nextImageNumber: (node.baseImage.sequenceNumber ?? state.nextImageNumber) + 1,
    });

    return node;
  },

  addNode: (payload) => {
    const state = get();
    const parentId = payload.parentId ?? state.currentNodeId;
    const parent = parentId ? state.nodesById[parentId] : null;

    if (!parent) {
      throw new Error("Cannot add an image node without a valid parent node.");
    }

    const node: ImageNode = {
      id: payload.id ?? createNodeId(),
      parentId: parent.id,
      baseImage: payload.baseImage ?? parent.generatedImage ?? parent.baseImage,
      generatedImage: {
        ...payload.generatedImage,
        sequenceNumber: payload.generatedImage.sequenceNumber ?? state.nextImageNumber,
      },
      maskData: payload.maskData,
      prompt: payload.prompt.trim(),
      childrenIds: [],
      createdAt: new Date().toISOString(),
    };

    set((current) => ({
      nodesById: {
        ...current.nodesById,
        [parent.id]: {
          ...parent,
          childrenIds: parent.childrenIds.includes(node.id)
            ? parent.childrenIds
            : [...parent.childrenIds, node.id],
        },
        [node.id]: node,
      },
      currentNodeId: node.id,
      rootNodeId: current.rootNodeId ?? parent.id,
      nextImageNumber: Math.max(current.nextImageNumber, (node.generatedImage?.sequenceNumber ?? current.nextImageNumber) + 1),
    }));

    return node;
  },

  navigateNode: (nodeId) => {
    if (!get().nodesById[nodeId]) {
      return;
    }
    set({ currentNodeId: nodeId });
  },

  getCurrentNode: () => {
    const { currentNodeId, nodesById } = get();
    return currentNodeId ? nodesById[currentNodeId] ?? null : null;
  },

  getAncestors: (nodeId) => {
    const { currentNodeId, nodesById } = get();
    const startId = nodeId ?? currentNodeId;
    const ancestors: ImageNode[] = [];
    let cursor = startId ? nodesById[startId] : null;

    while (cursor?.parentId) {
      const parent = nodesById[cursor.parentId];
      if (!parent) {
        break;
      }
      ancestors.unshift(parent);
      cursor = parent;
    }

    return ancestors;
  },

  resetTree: () => {
    set({
      nodesById: {},
      rootNodeId: null,
      currentNodeId: null,
      nextImageNumber: 1,
    });
  },
}));
