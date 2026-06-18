在前端静态文件里设置的，不是后端配置。
核心位置：
预设数据：web/src/app/image/image-presets.ts (line 1)
创作台引入预设：web/src/app/image/page.tsx (line 33)
预设卡片展示：web/src/app/image/components/image-results.tsx (line 347)
预设图片目录：web/public/presets
每个预设长这样：
ts



{
  id: "stellar-poster",
  title: "轮廓宇宙海报",
  prompt: "...",
  hint: "高审美叙事海报、角色宇宙主题视觉、收藏版概念海报。",
  imageSrc: "/presets/stellar-poster.webp",
  count: 1,
  size: "9:16",
}

字段含义：
id：唯一标识，不能重复。
title：卡片标题。
prompt：点击“套用这个预设”后填入输入框的提示词。
hint：卡片上的简短说明。
imageSrc：卡片封面图，同时点击后会作为参考图加载。
count：默认生成张数。
size：默认尺寸，可写 auto、1:1、4:3、9:16、16:9、1080p、2k、4k，也可以写类似 1024x1536 的宽高。
更换方式：
把新封面图放到 web/public/presets/，例如 my-preset.webp。
修改 image-presets.ts (line 10) 里的 IMAGE_PROMPT_PRESETS 数组。
替换或新增对象里的 title、prompt、hint、imageSrc、count、size。
执行构建：cd web && bun run build。
如果只想把当前四个换成新的四个，直接替换 IMAGE_PROMPT_PRESETS 数组里的四个对象即可。要显示更多或更少，也只需要增删这个数组项；展示区域是 map 自动渲染的，不需要额外改 UI。