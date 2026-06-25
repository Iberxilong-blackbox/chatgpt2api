export type RetouchMarkerColor = {
  id: string;
  name: string;
  css: string;
};

export function buildRetouchPrompt(userPrompt: string, markerColorName: string) {
  const instruction = userPrompt.trim();
  return [
    `参考输入图像，用户已用高对比度的${markerColorName}涂鸦/框线标出需要修改的区域。`,
    "",
    "仅对标注区域进行编辑，其余图像内容必须保持不变，包括人物、背景、光影、构图、色彩一致性。",
    "",
    "在标注区域内：",
    instruction,
    "",
    "要求：",
    "- 修改后的内容必须与原图光照、透视、风格一致",
    "- 边缘自然融合，无拼接感",
    "- 不改变未标注区域的细节",
    "- 保持整体画面真实一致性；如果原图是动漫或插画，则匹配原图风格",
    "",
    "high consistency, seamless blending, matching original style",
  ].join("\n");
}
