export type BananaPromptMode = "generate" | "edit";
export type PromptMarketSourceId = "banana-prompt-quicker";
export type PromptMarketLanguage = "zh-CN" | "en";

export type PromptMarketLocalization = {
  title: string;
  prompt: string;
  category: string;
  subCategory?: string;
};

export type BananaPrompt = {
  id: string;
  title: string;
  preview: string;
  referenceImageUrls: string[];
  prompt: string;
  author: string;
  link?: string;
  mode: BananaPromptMode;
  category: string;
  subCategory?: string;
  created?: string;
  source: PromptMarketSourceId;
  sourceLabel: string;
  isNsfw: boolean;
  localizations?: Partial<Record<PromptMarketLanguage, PromptMarketLocalization>>;
};

export const BANANA_PROMPTS_SOURCE_URL = "https://github.com/glidea/banana-prompt-quicker";
export const BANANA_PROMPTS_URL =
  "https://raw.githubusercontent.com/glidea/banana-prompt-quicker/main/prompts.json";
export const PROMPT_MARKET_SOURCE_OPTIONS: {
  value: PromptMarketSourceId;
  label: string;
}[] = [
  {
    value: "banana-prompt-quicker",
    label: "banana-prompt-quicker",
  },
];

type BananaPromptSourceItem = {
  title?: unknown;
  preview?: unknown;
  reference_image_urls?: unknown;
  prompt?: unknown;
  author?: unknown;
  link?: unknown;
  mode?: unknown;
  category?: unknown;
  sub_category?: unknown;
  created?: unknown;
};

const NSFW_TEXT_PATTERN =
  /\b(nsfw|nude|naked|lingerie|erotic|seductive|sexy|cleavage|underwear|panties|bra|bikini|ahegao|explicit|sensual|fetish|nipples?|genitals?|buttocks?|thong|topless)\b|裸|色情|情色|性感|诱惑|内衣|内裤|乳|胸|臀|私处|泳衣|比基尼|情趣|丁字裤|翻白眼|吐舌|妩媚|暧昧/i;

function normalizePromptMode(value: unknown): BananaPromptMode {
  return value === "edit" ? "edit" : "generate";
}

function buildPromptId(item: BananaPromptSourceItem, index: number) {
  return [item.title, item.author, index]
    .map((part) => String(part || "").trim())
    .filter(Boolean)
    .join(":");
}

function normalizeReferenceImageUrls(value: unknown) {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((url): url is string => typeof url === "string" && url.trim().length > 0);
}

function isNsfwPrompt(category: string, title: string, prompt: string) {
  return category === "NSFW" || NSFW_TEXT_PATTERN.test(`${category}\n${title}\n${prompt}`);
}

function normalizePrompt(item: BananaPromptSourceItem, index: number): BananaPrompt | null {
  if (
    typeof item.title !== "string" ||
    typeof item.preview !== "string" ||
    typeof item.prompt !== "string" ||
    typeof item.author !== "string"
  ) {
    return null;
  }

  const title = item.title.trim();
  const preview = item.preview.trim();
  const prompt = item.prompt.trim();
  const author = item.author.trim();
  const category =
    typeof item.category === "string" && item.category.trim() ? item.category.trim() : "未分类";
  if (!title || !preview || !prompt || !author) {
    return null;
  }

  return {
    id: `banana-prompt-quicker:${buildPromptId(item, index)}`,
    title,
    preview,
    prompt,
    author,
    referenceImageUrls: normalizeReferenceImageUrls(item.reference_image_urls),
    link: typeof item.link === "string" && item.link.trim() ? item.link.trim() : undefined,
    mode: normalizePromptMode(item.mode),
    category,
    subCategory: typeof item.sub_category === "string" && item.sub_category.trim() ? item.sub_category.trim() : undefined,
    created: typeof item.created === "string" && item.created.trim() ? item.created.trim() : undefined,
    source: "banana-prompt-quicker",
    sourceLabel: "banana-prompt-quicker",
    isNsfw: category === "NSFW",
  };
}

export async function fetchBananaPrompts(signal?: AbortSignal) {
  const response = await fetch(BANANA_PROMPTS_URL, {
    signal,
    headers: {
      Accept: "application/json",
    },
  });
  if (!response.ok) {
    throw new Error(`读取提示词市场失败：${response.status}`);
  }

  const data: unknown = await response.json();
  if (!Array.isArray(data)) {
    throw new Error("提示词市场数据格式无效");
  }

  return data.flatMap((item, index) => {
    const prompt = normalizePrompt(item as BananaPromptSourceItem, index);
    return prompt ? [prompt] : [];
  });
}

export async function fetchPromptMarketPrompts(signal?: AbortSignal) {
  return fetchBananaPrompts(signal);
}
