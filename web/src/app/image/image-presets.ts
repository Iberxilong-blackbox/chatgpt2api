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
    title: "轮廓宇宙海报",
    prompt:
      "请根据【主题：崩坏星穹铁道，角色卡芙卡】自动生成一张高审美的“轮廓宇宙 / 收藏版叙事海报”风格作品。不要将画面局限于固定器物或常见容器，不要优先默认瓶子、沙漏、玻璃罩、怀表之类的常规载体，而是由 AI 根据主题自行判断并选择一个最契合、最有象征意义、轮廓最强、最适合承载完整叙事世界的主轮廓载体。这个主轮廓可以是器物、建筑、门、塔、拱门、穹顶、楼梯井、长廊、雕像、侧脸、眼睛、手掌、头骨、羽翼、面具、镜面、王座、圆环、裂缝、光幕、阴影、几何结构、空间切面、舞台框景、抽象符号或其他更有创意与主题代表性的视觉轮廓，要求合理布局。优先选择最能放大主题气质、最能形成强烈视觉记忆点、最能体现史诗感、神秘感、诗意感或设计感的轮廓，而不是最安全、最普通、最常见的容器。画面的核心不是简单把世界装进某个物体里，而是让完整的主题世界自然生长在这个主轮廓之中、之内、之上、之边界里或与其结构融为一体，形成一种“主题宇宙依附于一个象征性轮廓展开”的高级叙事效果。主轮廓必须清晰、优雅、有辨识度，并在整体构图中占据核心地位。轮廓内部或边界中需要自动生成与主题强绑定的完整叙事世界，内容应当丰富、饱满、层次清晰，包括最能代表主题的标志性场景、核心建筑或空间结构、象征符号与隐喻元素、角色关系或文明痕迹、远景中景近景的空间递进、具有命运感和情绪张力的氛围层次，以及门、台阶、桥梁、水面、烟雾、路径、光源、遗迹、机械结构、自然景观、抽象形态、生物或道具等叙事细节。所有元素必须统一、自然、有主次、有层级地融合，像一个完整世界真实孕育在这个轮廓结构之中，而不是简单拼贴、裁切填充、素材堆叠或模板化背景。整体构图需要具有强烈的收藏版海报气质与高级设计感，大结构稳定，主轮廓强烈明确，内部世界具有纵深、秩序和呼吸感，细节丰富但不拥挤，内容丰满但不杂乱，可以适度加入小比例人物剪影、远处建筑、光柱、门洞、桥、阶梯、回廊、倒影、天光或远景结构来增强尺度感、故事感与史诗感。整体画面要安静、宏大、凝练、富有余味，不要平均铺满，不要廉价热闹，不要无重点堆砌。风格融合收藏版电影海报构图、高级叙事型视觉设计、梦幻水彩质感与纸张印刷品气质，强调纸张颗粒感、边缘飞白、水彩刷痕、轻微晕染、空气透视、柔和雾化、局部体积光、光雾穿透、大面积留白与克制版式，让画面看起来像设计师完成的高端收藏版视觉作品，而不是普通 AI 跑图。整体气质要高级、诗意、宏大、神圣、怀旧、安静、具有传说感和叙事感。色彩由 AI 根据主题自动判断并匹配最合适的高级配色方案，但必须保持统一、克制、耐看、低饱和、高级，不要杂乱高饱和，不要廉价霓虹感，不要塑料数码感。配色可以围绕黑金灰、冷蓝灰、雾白灰、褐红米白、暗铜、旧纸色、深海蓝、暮色紫、银灰等体系自由变化，但必须始终服务主题，并保持海报级审美与整体和谐。最终要求：第一眼有强烈的主题识别度和轮廓记忆点，第二眼有完整丰富的叙事世界，第三眼仍有细节和余味。轮廓选择必须具有创意和主题匹配度，尽量避免重复、保守、常见的容器套路，优先选择更有象征性、更有空间感、更有设计潜力的轮廓形式。不要普通背景拼接，不要生硬裁切，不要模板化奇幻素材，不要游戏宣传图感，不要过度卡通化，不要过度写实导致失去艺术感，不要形式大于内容。如果合适，可以自然加入低调克制的标题、编号、签名或落款，让它更像收藏版海报设计的一部分，但不要喧宾夺主。",
    hint: "高审美叙事海报、角色宇宙主题视觉、收藏版概念海报。",
    imageSrc: "/presets/stellar-poster.webp",
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
