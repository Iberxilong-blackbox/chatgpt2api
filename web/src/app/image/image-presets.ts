export type ImagePromptPreset = {
  id: string;
  title: string;
  prompt: string;
  hint: string;
  imageSrc: string;
  count: number;
  size: string;
};

export const IMAGE_PROMPT_PRESETS: ImagePromptPreset[] = [
  {
    id: "stellar-poster",
    title: "戏剧光影二次元",
    prompt:`CRITICAL DIRECTIVE: Create a masterpiece cinematic illustration fusing 1980s/1990s retro Japanese anime hand-drawn aesthetics with a high-fidelity 3D sculptural Unreal Engine render. The final image must project a powerful, oppressive atmosphere with an epic, tragic sense of destiny.

1. VISUAL STYLE & ART DIRECTION (新复古三维雕塑感动漫风格)
- Aesthetic Fusion: A unique neo-retro 3D sculptural style with high-contrast cel-shading, featuring strong, dramatic, and exaggerated thick black ink outlines contouring the model.
- Texture & Ambience: Covered in heavy analog film grain and decayed, mottled, and weathered textures. Blended with a hazy slow-shutter motion blur and a dreamcore-like layered depth of field.

2. COMPOSITION, ANGLE & FORESHORTENING (构图与动态透视)
- Camera & Perspective: Dynamic full-body shot, captured on an ultra-wide-angle lens from an extreme low angle, creating a powerful, forced perspective and high visual tension.
- Focal Depth: A compressed perspective that brings the subject close to the lens for an intense, close-up-like facial impact, while still revealing the full-body posture against a blurred, deeply layered atmospheric background.

3. SUBJECT & EMOTIONAL INTENSITY (主体与悲壮氛围)
- Subject: [SUBJECT, e.g., A battle-worn mecha warrior with broken mechanical wings / An ancient dark sorcerer holding a glowing cracked relic].
- Mood: Exquisite maximalist details, sharp structural lines, natural shadow gradients, flowing dynamic effects, and floating dispersed dust particles that evoke a deep, tragic sense of destiny and primal dread.

4. LIGHTING & FLUID ART OF SHADOW (戏剧性光影与流体艺术)
- Chiaroscuro Blueprint: High-contrast cinematic dark lighting (Rembrandt lighting) with deep, dramatic shadows.
- Glow & Flare: Blends overexposed, intense lens glare and soft diffused volumetric lights with sharp, razor-thin rim-lights that outline the subject.
- Intertwined Effect: The light and shadow must feel alive, creating a fluid art effect of chaotic, intertwined luminous paths drifting across the frame.

5. TECHNICAL PARAMETERS & NEGATIVE PROMPT
Negative: (modern generic CGI look, flat 2D vector art, safe corporate graphics, bright cheerful lighting, flawless plastic skin, smooth clean textures, generic anime style).

--ar 3:4 --style raw --v 6.0`,
    hint: "**1990年代的2D日本复古动漫+虚幻引擎渲染**",
    imageSrc: "/presets/UE-2.webp",
    count: 1,
    size: "9:16",
  },
  {
    id: "qinghua-museum-infographic",
    title: "青花瓷博物馆图鉴",
    prompt:
      "请根据“青花瓷”自动生成一张“博物馆图鉴式中文拆解信息图”。要求整张图兼具真实写实主视觉、结构拆解、中文标注、材质说明、纹样寓意、色彩含义和核心特征总结。你需要根据主题自动判断最合适的主体对象、服饰体系、器物结构、时代风格、关键部件、材质工艺、颜色方案与版式结构，用户无需再提供其他信息。整体风格应为：国家博物馆展板、历史服饰图鉴、文博专题信息图，而不是普通海报、古风写真、电商详情页或动漫插画。背景采用米白、绢纸白、浅茶色等纸张质感，整体高级、克制、专业、可收藏。版式固定为：顶部：中文主标题 + 副标题 + 导语；左侧：结构拆解区，中文引线标注关键部件，并配局部特写；右上：材质 / 工艺 / 质感区，展示真实纹理小样并附说明；右中：纹样 / 色彩 / 寓意区，展示主色板、纹样样本和文化解释；底部：穿着顺序 / 构成流程图 + 核心特征总结。若主题适合人物展示，则以真实人物全身站姿为中央主体；若更适合器物或单体结构，则改为中心主体拆解图，但整体仍保持完整中文信息图形式。所有文字必须为简体中文，清晰、规整、可读，不要乱码、错字、英文或拼音。重点突出真实结构、材质差异、文化说明与图鉴气质。避免：海报感、影楼感、电商感、动漫感、cosplay感、乱标注、错结构、糊字、假材质、过度装饰。",
    hint: "文博专题、器物拆解、中文信息图和展板式视觉。",
    imageSrc: "/presets/qinghua-museum-infographic.webp",
    count: 1,
    size: "4:3",
  },
  {
    id: "meining-ziyi",
    title: "凡人联动宣传图",
    prompt: `{
  "project_type": "cinematic 3D Chinese Xianxia animation theatrical key visual poster",
  "aspect_ratio": "9:16",
  "overall_aesthetic": "ethereal, classical Chinese Xianxia, soft and elegant, cinematic atmosphere, poetic and beautiful",
  
  "masthead": {
    "title_logo": "凡人修仙传",
    "english_title": "A Record of a Mortal's Journey to Immortality",
    "subtitle_character": "梅 凝 · MEI NING",
    "position": "top center",
    "typography": "traditional Chinese calligraphy font, elegant, golden-glowing strokes",
    "issue_metadata": "CHAPTER: YINMING REALM // THEATRICAL PROMO POSTER"
  },
  
  "hero_subject": {
    "character": "Preserve the exact face identity and facial features of Mei Ning from the uploaded image without alteration",
    "physique_feature": "A beautiful young immortal woman (19-22) with a demure and gentle expression, looking slightly away from the camera with soft, emotional eyes",
    "hair_style": "Neat classical Chinese updo with soft loose strands of hair framing her face, catching the rim light",
    "wardrobe": "Flowing traditional Hanfu robes in soft lavender-purple and pearl-white, delicate silk and chiffon fabric textures flowing gracefully",
    "spiritual_effect": "A subtle, soft-glowing ethereal white spiritual light (通灵之气) radiating gently around her shoulders and palms, representing her Phoenix Marrow body (通玉凤髓之体)"
  },
  
  "cover_lines_and_headlines": {
    "primary_tagline": {
      "text": "通玉凤髓，一世温婉",
      "position": "lower third, neatly arranged vertically or horizontally",
      "style": "clean traditional Chinese font, soft white glow with gold outline"
    },
    "sub_details": [
      {
        "text": "乱星海 · 阴冥之地",
        "position": "left middle side"
      },
      {
        "text": "漫漫修仙，凡人凡心",
        "position": "right middle side, vertical alignment"
      }
    ]
  },
  
  "composition_and_layout": {
    "background": "epic misty Xianxia mountains, swirling ethereal white clouds, traditional ink-wash silhouettes of distant cliffs, a hint of ancient Taoist pavilion in the fog",
    "foreground_accents": "floating translucent white jade petals and spiritual light particles drifting across the frame, creating atmospheric depth",
    "framing": "medium shot, upper body visible, elegant 3D cinematic depth of field, sharp focus on the character"
  },
  
  "chromatic_specification": {
    "primary_color": "#D2C4E3",
    "accent_colors": [
      "#FFFFFF",
      "#A3B8CC",
      "#E8DDF2",
      "#E5C8A0"
    ],
    "color_description": "A harmonious blend of soft lavender, mist blue, pearl white, with a warm golden sunset glow filtering through the background fog"
  },
  
  "technical_negatives": [
    "modern western clothing",
    "neon lights",
    "futuristic cyberpunk elements",
    "cheap flat 2D cartoon coloring",
    "ugly distorted face",
    "plastic skin texture",
    "messy alignment of Chinese text",
    "dark gothic atmosphere"
  ]
}`,
    hint: "JSON结构化设计海报。",
    imageSrc: "/presets/meining-ziyi.webp",
    count: 1,
    size: "9:16",
  },
  {
    id: "MossBrew",
    title: "后末日时代温室咖啡馆MossBrew",
    prompt: `CRITICAL DIRECTIVE: Create a breathtaking, high-impact official animation Key Visual (KV) poster based on the theme: "[Post-apocalyptic cozy greenhouse cafe run by a young cyberpunk witch, brewing glowing neon potions in espresso machines, surrounded by overgrown plants, ancient rusted robots acting as mossy flowerpots, warm golden sunlight streaming through cracked glass]". The composition must be structured like a professional theatrical anime promo poster, seamlessly weaving character, background, logo, and tagline into a single, cohesive cinematic narrative.

1. THEATRICAL KEY VISUAL LAYOUT & COMPOSITION
- Dynamic Layering: A masterful three-tier composition. 
  - Foreground: The main protagonist standing in a dynamic, heroic, or contemplative pose, looking slightly off-camera.
  - Midground: Supporting companions and key antagonist silhouettes framing the sides, creating a sense of conflict.
  - Background: A sweeping, epic-scale panoramic vista that immediately establishes the world's lore and setting.
- Composition Style: Golden triangle layout, strong sense of scale and depth of field, cinematic poster format.

2. LOGO, TYPOGRAPHY & PROMOTIONAL SLOGAN
- Anime Title Logo: A beautifully stylized, bold title logo integrated seamlessly at the [bottom center / top center] of the poster. The logo design must match the aesthetics of "[Post-apocalyptic cozy greenhouse cafe run by a young cyberpunk witch, brewing glowing neon potions in espresso machines, surrounded by overgrown plants, ancient rusted robots acting as mossy flowerpots, warm golden sunlight streaming through cracked glass]".
- English Subtitle & Slogan: Below the logo, include a clean, highly legible tagline in a minimalist sans-serif font. The tagline reads: "废土之上的温暖港湾".
- Studio Credits Block: A tiny, neat, out-of-focus credit block (cast & staff placeholders) at the very bottom edge to enhance the authentic theatrical poster feel.

3. THEME-SPECIFIC CHARACTER & SCENERY DETAILS
- Character Design: Highly appealing and charismatic anime character designs fitting the "[Post-apocalyptic cozy greenhouse cafe run by a young cyberpunk witch, brewing glowing neon potions in espresso machines, surrounded by overgrown plants, ancient rusted robots acting as mossy flowerpots, warm golden sunlight streaming through cracked glass]". Exquisite clothing details, hair catching the wind, and expressive eyes with intense focal depth.
- Environmental World-building: The background features epic scenery representing "[Post-apocalyptic cozy greenhouse cafe run by a young cyberpunk witch, brewing glowing neon potions in espresso machines, surrounded by overgrown plants, ancient rusted robots acting as mossy flowerpots, warm golden sunlight streaming through cracked glass]". Rich in textures, atmospheric particles (dust, embers, or flower petals) drifting across the frame.

4. CINEMATOGRAPHY, LIGHTING & PALETTE
- Dramatic Lighting: Volumetric god-rays, strong cinematic rim-lighting highlighting the characters' silhouettes, high-contrast chiaroscuro shadows.
- Color Grading: Professional movie-grade color palette designed for "[Post-apocalyptic cozy greenhouse cafe run by a young cyberpunk witch, brewing glowing neon potions in espresso machines, surrounded by overgrown plants, ancient rusted robots acting as mossy flowerpots, warm golden sunlight streaming through cracked glass]" (e.g., teal and orange, neon cyberpunk, or dark fantasy warm amber). Beautiful color harmony and depth.
- Camera Spec: Shot on anamorphic lens, shallow depth of field, subtle organic film grain, theatrical anime movie quality.

5. TECHNICAL LIMITATIONS & NEGATIVE PROMPT
Negative: (messy gibberish text overlapping characters, chaotic layout, ugly font, blurry logos, casual snapshot, low-quality fan art, deformed faces, bad anatomy, flat lighting, 2D flat coloring with no depth, 3D CGI plastic render).

--ar 9:16 --style raw --v 6.0`,
    hint: "关键视觉（KV）主题电影海报",
    imageSrc: "/presets/MossBrew.webp",
    count: 1,
    size: "16:9",
  },
];
