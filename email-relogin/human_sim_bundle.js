// Human Behavior Simulator — IIFE Bundle
// Generated from autoMer/build/human-simulator/
// Source files merged: math.js, dom.js, behavior.js, interaction.js, events.js, index.js
// Injected via Playwright page.evaluate() — runs in browser context.
(function () {
  'use strict';

  // ==================== utils/math.js ====================
  // Box-Muller transform for normal distribution random numbers
  function normalRandom(min, max) {
    const u1 = Math.random();
    const u2 = Math.random();
    const z0 = Math.sqrt(-2 * Math.log(u1)) * Math.cos(2 * Math.PI * u2);
    const mean = (min + max) / 2;
    const stdDev = (max - min) / 6; // 99.7% values within 3σ
    let result = z0 * stdDev + mean;
    return Math.max(min, Math.min(max, Math.round(result)));
  }

  // Calculate distance between two points
  function calculateDistance(point1, point2) {
    const dx = point2.x - point1.x;
    const dy = point2.y - point1.y;
    return Math.sqrt(dx * dx + dy * dy);
  }

  // Cubic bezier curve formula
  function cubicBezier(p0, p1, p2, p3, t) {
    const x = Math.pow(1 - t, 3) * p0.x +
              3 * Math.pow(1 - t, 2) * t * p1.x +
              3 * (1 - t) * Math.pow(t, 2) * p2.x +
              Math.pow(t, 3) * p3.x;
    const y = Math.pow(1 - t, 3) * p0.y +
              3 * Math.pow(1 - t, 2) * t * p1.y +
              3 * (1 - t) * Math.pow(t, 2) * p2.y +
              Math.pow(t, 3) * p3.y;
    return { x, y };
  }

  // Get perpendicular unit vector
  function getPerpendicularVector(start, end) {
    const dx = end.x - start.x;
    const dy = end.y - start.y;
    const length = Math.sqrt(dx * dx + dy * dy);
    if (length === 0) {
      return { x: 0, y: 1 }; // default perpendicular
    }
    // Perpendicular vector (clockwise 90 degrees)
    return { x: -dy / length, y: dx / length };
  }

  // Calculate string similarity (Levenshtein distance)
  function calculateSimilarity(str1, str2) {
    const len1 = str1.length;
    const len2 = str2.length;
    const matrix = Array(len1 + 1).fill().map(() => Array(len2 + 1).fill(0));
    for (let i = 0; i <= len1; i++) matrix[i][0] = i;
    for (let j = 0; j <= len2; j++) matrix[0][j] = j;
    for (let i = 1; i <= len1; i++) {
      for (let j = 1; j <= len2; j++) {
        if (str1.charAt(i - 1) === str2.charAt(j - 1)) {
          matrix[i][j] = matrix[i - 1][j - 1];
        } else {
          matrix[i][j] = Math.min(
            matrix[i - 1][j - 1] + 1,
            matrix[i][j - 1] + 1,
            matrix[i - 1][j] + 1
          );
        }
      }
    }
    const distance = matrix[len1][len2];
    const maxLen = Math.max(len1, len2);
    return maxLen === 0 ? 1 : (maxLen - distance) / maxLen;
  }

  // ==================== utils/dom.js ====================
  // Validate click coordinate safety
  function validateClickCoordinate(coords, rect) {
    const { x, y } = coords;
    // Basic boundary check
    if (x < rect.left || x >= rect.right || y < rect.top || y >= rect.bottom) {
      return false;
    }
    // Check if too close to edge (may cause click failure)
    const marginX = Math.min(3, rect.width * 0.05);
    const marginY = Math.min(3, rect.height * 0.05);
    if (x < rect.left + marginX || x > rect.right - marginX ||
        y < rect.top + marginY || y > rect.bottom - marginY) {
      return true; // near edge but still valid
    }
    return true;
  }

  // ==================== behavior.js ====================

  // Poisson distribution timing simulator
  class PoissonTimingSimulator {
    constructor(logger) {
      this.logger = logger || { debug: () => {}, info: () => {}, warn: () => {}, error: () => {} };
      this.lambdaValues = {
        'reading': 0.3,        // one reading-related event every 3.3s
        'typing': 1.5,         // one typing event every 0.67s
        'browsing': 0.8,       // one browsing event every 1.25s
        'decision_making': 0.2, // one decision event every 5s
        'mouse_movement': 0.6, // one mouse movement every 1.67s
        'idle': 0.1            // one random event every 10s during idle
      };
      this.currentHour = new Date().getHours();
    }

    // Generate poisson-distributed interval (milliseconds)
    poissonInterval(activityType = 'typing') {
      const lambda = this.lambdaValues[activityType] || 0.5;
      // Poisson process: next event time = -ln(1-U)/λ
      const u = Math.random();
      const intervalSeconds = -Math.log(1.0 - u) / lambda;
      let intervalMs = intervalSeconds * 1000;
      intervalMs = this.adjustForTimeOfDay(intervalMs, this.currentHour);
      const minInterval = this.getMinInterval(activityType);
      const maxInterval = this.getMaxInterval(activityType);
      const finalInterval = Math.max(minInterval, Math.min(maxInterval, intervalMs));
      return Math.round(finalInterval);
    }

    // Generate poisson event sequence over a duration
    generateEventSequence(duration, activityType = 'typing') {
      const events = [];
      let currentTime = 0;
      while (currentTime < duration) {
        const interval = this.poissonInterval(activityType);
        currentTime += interval;
        if (currentTime < duration) {
          events.push({ timestamp: currentTime, type: activityType, interval: interval });
        }
      }
      return events;
    }

    // Adjust interval based on time of day (circadian rhythm)
    adjustForTimeOfDay(baseInterval, hour) {
      const activityCurve = {
        0: 0.1, 1: 0.05, 2: 0.03, 3: 0.02, 4: 0.02, 5: 0.03,
        6: 0.1, 7: 0.3, 8: 0.6, 9: 0.9, 10: 1.0, 11: 1.0,
        12: 0.8, 13: 0.9, 14: 1.0, 15: 0.9, 16: 0.8, 17: 0.7,
        18: 0.6, 19: 0.5, 20: 0.4, 21: 0.3, 22: 0.2, 23: 0.15
      };
      const activityMultiplier = activityCurve[hour] || 0.5;
      return baseInterval / activityMultiplier;
    }

    getMinInterval(activityType) {
      const minIntervals = {
        'reading': 500, 'typing': 50, 'browsing': 200,
        'decision_making': 1000, 'mouse_movement': 100, 'idle': 2000
      };
      return minIntervals[activityType] || 100;
    }

    getMaxInterval(activityType) {
      const maxIntervals = {
        'reading': 10000, 'typing': 500, 'browsing': 5000,
        'decision_making': 30000, 'mouse_movement': 2000, 'idle': 60000
      };
      return maxIntervals[activityType] || 5000;
    }

    // Hybrid: mix poisson and normal distribution for more randomness
    hybridDelay(activityType = 'typing', poissonWeight = 0.4) {
      if (Math.random() < poissonWeight) {
        return this.poissonInterval(activityType);
      } else {
        const minInterval = this.getMinInterval(activityType);
        const maxInterval = this.getMaxInterval(activityType);
        return normalRandom(minInterval, maxInterval * 0.8);
      }
    }

    updateCurrentHour() {
      this.currentHour = new Date().getHours();
    }
  }

  // Cognitive delay simulator
  class CognitiveDelaySimulator {
    constructor(logger) {
      this.logger = logger || { debug: () => {}, info: () => {}, warn: () => {}, error: () => {} };
      this.delayProfiles = {
        reading:             { min: 1200, max: 3500, description: "Reading comprehension delay" },
        thinking:            { min: 800,  max: 2200, description: "Thinking/organizing delay" },
        hesitation:          { min: 300,  max: 1500, description: "Hesitation delay" },
        typing_preparation:  { min: 200,  max: 800,  description: "Finger positioning delay" },
        word_pause:          { min: 50,   max: 400,  description: "Inter-word pause" },
        sentence_pause:      { min: 200,  max: 800,  description: "Inter-sentence thinking time" },
        comprehension_pause: { min: 500,  max: 1800, description: "Complex info comprehension pause" },
        decision_pause:      { min: 1000, max: 4000, description: "Decision-making pause" }
      };
    }

    calculateReadingDelay(text) {
      const baseDelay = this.delayProfiles.reading;
      const wordsPerMinute = 200; // average reading speed (Chinese characters)
      const charCount = text ? text.length : 0;
      if (charCount === 0) return baseDelay.min;
      const calculatedDelay = (charCount / 3) / wordsPerMinute * 60 * 1000;
      const randomFactor = 0.3 + Math.random() * 0.4;
      return Math.round(Math.max(baseDelay.min, Math.min(baseDelay.max, calculatedDelay * randomFactor)));
    }

    calculateThinkingDelay(inputText, context = 'normal') {
      const baseDelay = this.delayProfiles.thinking;
      const complexityFactors = {
        'simple_greeting': 0.3, 'normal': 1.0, 'technical': 1.5,
        'negotiation': 2.0, 'complaint': 1.8, 'explanation': 1.6
      };
      const factor = complexityFactors[context] || 1.0;
      const lengthFactor = Math.min(2.0, inputText.length / 50);
      return normalRandom(baseDelay.min * factor, baseDelay.max * factor * lengthFactor);
    }

    calculateHesitationDelay(context = 'normal', confidence = 0.5) {
      const baseDelay = this.delayProfiles.hesitation;
      const confidenceFactor = 1.5 - confidence;
      const contextFactors = {
        'typing_start': 1.2, 'word_choice': 0.8, 'click_target': 1.0,
        'form_submit': 1.5, 'normal': 1.0
      };
      const contextFactor = contextFactors[context] || 1.0;
      const totalFactor = confidenceFactor * contextFactor;
      return normalRandom(baseDelay.min * totalFactor, baseDelay.max * totalFactor);
    }

    calculateInterCharDelay(currentChar, nextChar, position, fullText) {
      const base = this.delayProfiles.word_pause;
      // Chinese punctuation pause
      if (/[。！？，、；：]/.test(currentChar)) {
        const sentencePause = this.delayProfiles.sentence_pause;
        return normalRandom(sentencePause.min, sentencePause.max);
      }
      // English punctuation
      if (/[.!?]/.test(currentChar)) {
        const sentencePause = this.delayProfiles.sentence_pause;
        return normalRandom(sentencePause.min * 0.8, sentencePause.max * 0.8);
      }
      // Word boundary (space, end of English word)
      if (currentChar === ' ' || (/[a-zA-Z]/.test(currentChar) && !/[a-zA-Z]/.test(nextChar))) {
        return normalRandom(base.min * 1.5, base.max * 1.5);
      }
      // Number-to-non-number transition
      if (/[0-9]/.test(currentChar) && !/[0-9]/.test(nextChar)) {
        return normalRandom(base.min * 1.2, base.max * 1.2);
      }
      // Normal inter-character delay
      return normalRandom(base.min, base.max);
    }

    analyzeInputComplexity(text) {
      const analysis = {
        charCount: text.length,
        wordCount: text.split(/\s+/).length,
        sentenceCount: (text.match(/[。！？.!?]/g) || []).length,
        hasNumbers: /\d/.test(text),
        hasEmail: /@/.test(text),
        hasUrl: /http|www/.test(text),
        complexity: 'normal'
      };
      if (analysis.hasEmail || analysis.hasUrl) { analysis.complexity = 'technical'; }
      else if (analysis.charCount > 100) { analysis.complexity = 'explanation'; }
      else if (text.includes('薪') || text.includes('钱') || text.includes('工资')) { analysis.complexity = 'negotiation'; }
      else if (text.includes('问题') || text.includes('不满') || text.includes('投诉')) { analysis.complexity = 'complaint'; }
      else if (analysis.charCount < 20) { analysis.complexity = 'simple_greeting'; }
      return analysis;
    }

    generateIntelligentInputDelay(inputText, previousMessage = '', context = 'normal') {
      const analysis = this.analyzeInputComplexity(inputText);
      const readingDelay = this.calculateReadingDelay(previousMessage);
      const thinkingDelay = this.calculateThinkingDelay(inputText, analysis.complexity);
      const preparationDelay = normalRandom(
        this.delayProfiles.typing_preparation.min, this.delayProfiles.typing_preparation.max
      );
      let hesitationDelay = 0;
      if (analysis.complexity !== 'simple_greeting' && Math.random() < 0.3) {
        hesitationDelay = this.calculateHesitationDelay('typing_start', 0.7);
      }
      const totalDelay = readingDelay + thinkingDelay + preparationDelay + hesitationDelay;
      return {
        total: totalDelay,
        breakdown: { reading: readingDelay, thinking: thinkingDelay, preparation: preparationDelay, hesitation: hesitationDelay },
        analysis: analysis
      };
    }
  }

  // Human behavior simulator core
  class HumanBehaviorSimulator {
    constructor(options = {}) {
      this.logger = options.logger || { debug: () => {}, info: () => {}, warn: () => {}, error: () => {} };
      this.storage = options.storage || new MemoryStorage();
      this.initialProfile = options.initialProfile || null;
      this.poissonTiming = new PoissonTimingSimulator(this.logger);
      this.cognitiveDelay = new CognitiveDelaySimulator(this.logger);
      this.currentActivity = 'idle';
    }

    async initRandomSeed() {
      const savedSeed = await this.storage.get('behavior_seed', Date.now());
      this.seed = savedSeed;
      await this.storage.set('behavior_seed', this.seed);
    }

    generateHumanDelay(baseMin = 100, baseMax = 300, context = 'normal') {
      const patterns = {
        'thinking': { min: 2000, max: 5000, activity: 'decision_making' },
        'reading': { min: 1000, max: 3000, activity: 'reading' },
        'typing': { min: 50, max: 200, activity: 'typing' },
        'clicking': { min: 40, max: 120, activity: 'browsing' },
        'mouse_movement': { min: 100, max: 800, activity: 'mouse_movement' },
        'normal': { min: baseMin, max: baseMax, activity: 'browsing' }
      };
      const pattern = patterns[context] || patterns.normal;
      if (Math.random() < 0.4 && this.poissonTiming) {
        return this.poissonTiming.hybridDelay(pattern.activity || context, 0.6);
      } else {
        return normalRandom(pattern.min, pattern.max);
      }
    }

    async loadBehaviorProfile() {
      if (this.initialProfile) {
        this.behaviorPatterns = this.initialProfile;
        return;
      }
      const saved = await this.storage.get('behavior_profile', null);
      if (saved) {
        this.behaviorPatterns = typeof saved === 'string' ? JSON.parse(saved) : saved;
        return;
      }
      const profile = this.generatePersonalityProfile();
      await this.storage.set('behavior_profile', JSON.stringify(profile));
      this.behaviorPatterns = profile;
    }

    generatePersonalityProfile() {
      const personalities = ['quick', 'normal', 'careful', 'hesitant'];
      const personality = personalities[Math.floor(Math.random() * personalities.length)];
      const profiles = {
        'quick': { speed: 0.7, accuracy: 0.9, pause_frequency: 0.1 },
        'normal': { speed: 1.0, accuracy: 0.95, pause_frequency: 0.2 },
        'careful': { speed: 1.3, accuracy: 0.98, pause_frequency: 0.3 },
        'hesitant': { speed: 1.8, accuracy: 0.85, pause_frequency: 0.4 }
      };
      return { type: personality, ...profiles[personality], created_at: Date.now() };
    }

    setCurrentActivity(activityType) {
      this.poissonTiming.updateCurrentHour();
      this.currentActivity = activityType;
    }

    generateContextualDelay(context = null) {
      const activity = context || this.currentActivity;
      return this.poissonTiming.hybridDelay(activity, 0.5);
    }

    getCurrentActivity() { return this.currentActivity; }

    generateIntelligentDelay(activityType) {
      this.setCurrentActivity(activityType);
      const personalityFactor = this.behaviorPatterns ? this.behaviorPatterns.speed : 1.0;
      const baseDelay = this.poissonTiming.hybridDelay(activityType);
      return Math.round(baseDelay * personalityFactor);
    }

    static async create(options = {}) {
      const simulator = new HumanBehaviorSimulator(options);
      await simulator.initRandomSeed();
      await simulator.loadBehaviorProfile();
      return simulator;
    }
  }

  // Default memory storage
  class MemoryStorage {
    constructor() { this._store = new Map(); }
    async get(key, defaultValue) { return this._store.has(key) ? this._store.get(key) : defaultValue; }
    async set(key, value) { this._store.set(key, value); }
  }

  // ==================== interaction.js ====================

  // Bezier curve mouse trajectory generator
  class MouseTrajectoryGenerator {
    constructor(humanBehavior, logger) {
      this.humanBehavior = humanBehavior;
      this.logger = logger || { debug: () => {}, info: () => {}, warn: () => {}, error: () => {} };
      this.lastMousePosition = { x: 0, y: 0 };
      this.isInitialized = false;
    }

    initializeMousePosition() {
      if (!this.isInitialized) {
        this.lastMousePosition = { x: window.innerWidth / 2, y: window.innerHeight / 2 };
        this.isInitialized = true;
      }
    }

    generateBezierPath(startPoint, endPoint, duration = 800) {
      this.initializeMousePosition();
      const distance = calculateDistance(startPoint, endPoint);
      const baseCurvature = Math.min(distance * 0.4, 150);
      const randomFactor = 0.7 + Math.random() * 0.6;
      const curvature = baseCurvature * randomFactor;
      const speedFactor = Math.max(0.5, Math.min(2.0, 800 / duration));
      const controlPoint1 = this.generateControlPoint(startPoint, endPoint, 0.25, curvature * speedFactor);
      const controlPoint2 = this.generateControlPoint(startPoint, endPoint, 0.75, curvature * speedFactor);
      return this.calculateBezierPoints(startPoint, controlPoint1, controlPoint2, endPoint, duration);
    }

    calculateBezierPoints(p0, p1, p2, p3, duration) {
      const points = [];
      const steps = Math.max(8, Math.floor(duration / 20)); // 50fps, min 8 points
      for (let i = 0; i <= steps; i++) {
        const t = i / steps;
        const point = cubicBezier(p0, p1, p2, p3, t);
        const timestamp = (duration / steps) * i;
        // Micro human hand tremor
        const jitter = normalRandom(-0.5, 0.5);
        point.x += jitter;
        point.y += jitter;
        points.push({ x: Math.round(point.x), y: Math.round(point.y), timestamp });
      }
      return points;
    }

    generateControlPoint(start, end, ratio, curvature) {
      const baseX = start.x + (end.x - start.x) * ratio;
      const baseY = start.y + (end.y - start.y) * ratio;
      const perpendicular = getPerpendicularVector(start, end);
      const randomOffset = (Math.random() - 0.5) * curvature;
      const bp = this.humanBehavior.behaviorPatterns;
      const personalityMultiplier = bp && bp.type === 'quick' ? 1.5 :
                                    bp && bp.type === 'careful' ? 1.2 :
                                    bp && bp.type === 'hesitant' ? 1.4 : 1.0;
      const personalityOffset = randomOffset * personalityMultiplier;
      return { x: baseX + perpendicular.x * personalityOffset, y: baseY + perpendicular.y * personalityOffset };
    }

    async simulateMouseTrajectory(trajectory) {
      if (!trajectory || trajectory.length === 0) return;
      let lastTimestamp = 0;
      for (const point of trajectory) {
        const stepDelay = point.timestamp - lastTimestamp;
        if (stepDelay > 0) { await this.sleep(stepDelay); }
        const element = document.elementFromPoint(point.x, point.y);
        if (element) {
          element.dispatchEvent(new MouseEvent('mousemove', {
            bubbles: true,
            clientX: point.x, clientY: point.y,
            movementX: point.x - this.lastMousePosition.x,
            movementY: point.y - this.lastMousePosition.y
          }));
        }
        lastTimestamp = point.timestamp;
      }
      const finalPoint = trajectory[trajectory.length - 1];
      this.lastMousePosition = { x: finalPoint.x, y: finalPoint.y };
    }

    getCurrentMousePosition() { return { ...this.lastMousePosition }; }
    setMousePosition(x, y) { this.lastMousePosition = { x, y }; this.isInitialized = true; }
    sleep(ms) { return new Promise(resolve => setTimeout(resolve, ms)); }
  }

  // Human error and hesitation behavior simulator
  class HumanErrorSimulator {
    constructor(eventSimulator, logger) {
      this.eventSimulator = eventSimulator;
      this.logger = logger || { debug: () => {}, info: () => {}, warn: () => {}, error: () => {} };
      this.errorRates = {
        typing: 0.08, click_hesitation: 0.15, misclick: 0.03,
        scroll_drift: 0.12, backspace_correction: 0.05
      };
      // Chinese character adjacency map (common pinyin input errors)
      this.chineseErrorMap = {
        '您': ['你', '林', '咱'], '好': ['号', '豪', '毫'],
        '工': ['个', '公', '攻'], '作': ['做', '坐', '座'],
        '时': ['市', '是', '室'], '间': ['件', '简', '见'],
        '薪': ['新', '心', '信'], '资': ['字', '子', '自']
      };
      // English keyboard adjacency
      this.keyboardAdjacency = {
        'q': ['w', 'a'], 'w': ['q', 'e', 's'], 'e': ['w', 'r', 'd'],
        'r': ['e', 't', 'f'], 't': ['r', 'y', 'g'], 'y': ['t', 'u', 'h'],
        'u': ['y', 'i', 'j'], 'i': ['u', 'o', 'k'], 'o': ['i', 'p', 'l'],
        'p': ['o', 'l'], 'a': ['q', 's', 'z'], 's': ['a', 'd', 'w', 'x'],
        'd': ['s', 'f', 'e', 'c'], 'f': ['d', 'g', 'r', 'v'],
        'g': ['f', 'h', 't', 'b'], 'h': ['g', 'j', 'y', 'n'],
        'j': ['h', 'k', 'u', 'm'], 'k': ['j', 'l', 'i'],
        'l': ['k', 'o', 'p'], 'z': ['a', 'x'], 'x': ['z', 'c', 's'],
        'c': ['x', 'v', 'd'], 'v': ['c', 'b', 'f'], 'b': ['v', 'n', 'g'],
        'n': ['b', 'm', 'h'], 'm': ['n', 'j']
      };
    }

    async simulateTypingWithErrors(element, text, errorRate = null) {
      errorRate = errorRate || this.errorRates.typing;
      const errors = this.generateTypingErrors(text, errorRate);
      element.focus();
      let currentValue = element.value || element.textContent || '';
      for (const action of errors) {
        if (action.type === 'type') {
          currentValue += action.char;
          await this.typeCharacterToElement(element, action.char, currentValue);
        } else if (action.type === 'error') {
          currentValue += action.wrongChar;
          await this.typeCharacterToElement(element, action.wrongChar, currentValue);
          const reactionTime = normalRandom(200, 800);
          await this.eventSimulator.sleep(reactionTime);
          currentValue = currentValue.slice(0, -1);
          await this.simulateBackspace(element, currentValue);
          const correctionPause = normalRandom(100, 400);
          await this.eventSimulator.sleep(correctionPause);
          currentValue += action.correctChar;
          await this.typeCharacterToElement(element, action.correctChar, currentValue);
        }
        const hb = this.eventSimulator.humanBehavior;
        if (hb && hb.cognitiveDelay) {
          const nextIndex = errors.indexOf(action) + 1;
          const nextChar = nextIndex < errors.length ? errors[nextIndex].char || errors[nextIndex].correctChar : '';
          const currentChar = action.char || action.correctChar;
          const interCharDelay = hb.cognitiveDelay.calculateInterCharDelay(currentChar, nextChar, action.position, text);
          await this.eventSimulator.sleep(interCharDelay);
        } else {
          await this.eventSimulator.sleep(normalRandom(50, 200));
        }
      }
      return true;
    }

    generateTypingErrors(text, errorRate) {
      const actions = [];
      for (let i = 0; i < text.length; i++) {
        const char = text[i];
        if (Math.random() < errorRate && char !== ' ') {
          const wrongChar = this.getAdjacentChar(char);
          actions.push({ type: 'error', wrongChar: wrongChar, correctChar: char, position: i });
        } else {
          actions.push({ type: 'type', char: char, position: i });
        }
      }
      return actions;
    }

    getAdjacentChar(char) {
      if (/[\u4e00-\u9fff]/.test(char)) {
        const alternatives = this.chineseErrorMap[char];
        if (alternatives && alternatives.length > 0) {
          return alternatives[Math.floor(Math.random() * alternatives.length)];
        }
        return this.getSimilarChineseChar(char);
      }
      if (/[a-zA-Z]/.test(char)) {
        const adjacent = this.keyboardAdjacency[char.toLowerCase()];
        if (adjacent && adjacent.length > 0) {
          const wrongChar = adjacent[Math.floor(Math.random() * adjacent.length)];
          return char === char.toUpperCase() ? wrongChar.toUpperCase() : wrongChar;
        }
      }
      if (/[0-9]/.test(char)) {
        const num = parseInt(char);
        const adjacent = [];
        if (num > 0) adjacent.push((num - 1).toString());
        if (num < 9) adjacent.push((num + 1).toString());
        if (adjacent.length > 0) return adjacent[Math.floor(Math.random() * adjacent.length)];
      }
      return this.getRandomSimilarChar(char);
    }

    getSimilarChineseChar(char) {
      const similarChars = ['的', '了', '是', '我', '你', '他', '她', '它', '们', '在'];
      return similarChars[Math.floor(Math.random() * similarChars.length)];
    }

    getRandomSimilarChar(char) {
      const similarChars = 'abcdefghijklmnopqrstuvwxyz0123456789';
      return similarChars[Math.floor(Math.random() * similarChars.length)];
    }

    async simulateClickHesitation(element, targetCoords) {
      if (Math.random() > this.errorRates.click_hesitation) { return false; }
      const rect = element.getBoundingClientRect();
      const hesitationCount = 2 + Math.floor(Math.random() * 3);
      for (let i = 0; i < hesitationCount; i++) {
        const wanderRadius = 15 + Math.random() * 25;
        const angle = Math.random() * 2 * Math.PI;
        const wanderPoint = {
          x: targetCoords.x + Math.cos(angle) * wanderRadius,
          y: targetCoords.y + Math.sin(angle) * wanderRadius
        };
        wanderPoint.x = Math.max(rect.left, Math.min(rect.right, wanderPoint.x));
        wanderPoint.y = Math.max(rect.top, Math.min(rect.bottom, wanderPoint.y));
        const es = this.eventSimulator;
        if (es.trajectoryGenerator) {
          const currentPos = es.trajectoryGenerator.getCurrentMousePosition();
          const hesitationTrajectory = es.trajectoryGenerator.generateBezierPath(currentPos, wanderPoint, 200 + Math.random() * 300);
          await es.trajectoryGenerator.simulateMouseTrajectory(hesitationTrajectory);
          es.trajectoryGenerator.setMousePosition(wanderPoint.x, wanderPoint.y);
        }
        const hesitationTime = normalRandom(150, 600);
        await this.eventSimulator.sleep(hesitationTime);
      }
      return true;
    }

    async simulateMisclick(targetElement) {
      if (Math.random() > this.errorRates.misclick) { return false; }
      const nearbyElements = this.findNearbyClickableElements(targetElement);
      if (nearbyElements.length === 0) return false;
      const wrongTarget = nearbyElements[Math.floor(Math.random() * nearbyElements.length)];
      await this.eventSimulator.simulateHumanClick(wrongTarget);
      const realizationTime = normalRandom(500, 1200);
      await this.eventSimulator.sleep(realizationTime);
      return true;
    }

    findNearbyClickableElements(targetElement) {
      const targetRect = targetElement.getBoundingClientRect();
      const allClickable = document.querySelectorAll(
        'button, a, input[type="button"], input[type="submit"], [onclick], [role="button"]'
      );
      const nearby = [];
      const searchRadius = 100;
      allClickable.forEach(element => {
        if (element === targetElement) return;
        const rect = element.getBoundingClientRect();
        const distance = Math.sqrt(
          Math.pow(rect.left - targetRect.left, 2) + Math.pow(rect.top - targetRect.top, 2)
        );
        if (distance <= searchRadius && rect.width > 0 && rect.height > 0) {
          nearby.push(element);
        }
      });
      return nearby.slice(0, 3);
    }

    async typeCharacterToElement(element, char, newValue) {
      element.dispatchEvent(new KeyboardEvent('keydown', { key: char, bubbles: true }));
      if (element.value !== undefined) { element.value = newValue; }
      else { element.textContent = newValue; }
      element.dispatchEvent(new Event('input', { bubbles: true }));
      element.dispatchEvent(new KeyboardEvent('keyup', { key: char, bubbles: true }));
    }

    async simulateBackspace(element, newValue) {
      element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Backspace', keyCode: 8, bubbles: true }));
      if (element.value !== undefined) { element.value = newValue; }
      else { element.textContent = newValue; }
      element.dispatchEvent(new Event('input', { bubbles: true }));
      element.dispatchEvent(new KeyboardEvent('keyup', { key: 'Backspace', keyCode: 8, bubbles: true }));
    }

    async simulateUnintentionalScroll() {
      if (Math.random() > this.errorRates.scroll_drift) { return false; }
      const scrollDistance = normalRandom(50, 200);
      const scrollDirection = Math.random() < 0.5 ? 1 : -1;
      window.scrollBy({ top: scrollDistance * scrollDirection, behavior: 'smooth' });
      await this.eventSimulator.sleep(1000 + Math.random() * 2000);
      if (Math.random() < 0.4) {
        window.scrollBy({ top: -scrollDistance * scrollDirection * 0.8, behavior: 'smooth' });
      }
      return true;
    }

    setErrorRates(newRates) { Object.assign(this.errorRates, newRates); }
    getErrorRates() { return { ...this.errorRates }; }
  }

  // ==================== events.js ====================

  // Top-level event orchestrator integrating mouse trajectory, clicks, typing, and error simulation
  class AntiStatisticEventSimulator {
    constructor(humanBehavior, logger) {
      this.humanBehavior = humanBehavior;
      this.logger = logger || { debug: () => {}, info: () => {}, warn: () => {}, error: () => {} };
      this.lastClickTime = 0;
      this.trajectoryGenerator = new MouseTrajectoryGenerator(humanBehavior, this.logger);
      this.errorSimulator = new HumanErrorSimulator(this, this.logger);
    }

    generateHumanClickCoordinate(rect) {
      if (!rect || rect.width <= 0 || rect.height <= 0) {
        return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2) };
      }
      const minPadding = 2;
      const maxPadding = Math.min(8, Math.floor(rect.width * 0.1), Math.floor(rect.height * 0.1));
      const padding = Math.max(minPadding, maxPadding);
      const safeLeft = rect.left + padding;
      const safeTop = rect.top + padding;
      const safeWidth = Math.max(6, rect.width - padding * 2);
      const safeHeight = Math.max(6, rect.height - padding * 2);
      let x, y;
      if (rect.width < 50 || rect.height < 30) {
        x = safeLeft + safeWidth * (0.35 + Math.random() * 0.3);
        y = safeTop + safeHeight * (0.35 + Math.random() * 0.3);
      } else {
        if (Math.random() < 0.6) {
          x = safeLeft + safeWidth * (0.15 + Math.random() * 0.35);
          y = safeTop + safeHeight * (0.15 + Math.random() * 0.35);
        } else {
          x = safeLeft + Math.random() * safeWidth;
          y = safeTop + Math.random() * safeHeight;
        }
      }
      const noiseRange = Math.min(1, rect.width * 0.02, rect.height * 0.02);
      x = Math.round(x + normalRandom(-noiseRange, noiseRange));
      y = Math.round(y + normalRandom(-noiseRange, noiseRange));
      x = Math.max(rect.left + 1, Math.min(rect.right - 1, x));
      y = Math.max(rect.top + 1, Math.min(rect.bottom - 1, y));
      if (x < rect.left || x >= rect.right || y < rect.top || y >= rect.bottom) {
        return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2) };
      }
      return { x, y };
    }

    async simulateHumanClick(element) {
      if (!element) return false;
      try {
        // 1. Check for misclick
        const hadMisclick = await this.errorSimulator.simulateMisclick(element);
        // 2. Get target coordinates
        const rect = element.getBoundingClientRect();
        const targetCoords = this.generateHumanClickCoordinate(rect);
        // 3. Generate bezier mouse trajectory
        const startPosition = this.trajectoryGenerator.getCurrentMousePosition();
        const movementDuration = this.humanBehavior.generateIntelligentDelay('mouse_movement');
        const trajectory = this.trajectoryGenerator.generateBezierPath(startPosition, targetCoords, movementDuration);
        // 4. Simulate mouse trajectory movement
        await this.trajectoryGenerator.simulateMouseTrajectory(trajectory);
        // 5. Possible click hesitation
        const hadHesitation = await this.errorSimulator.simulateClickHesitation(element, targetCoords);
        if (hadHesitation) {
          const finalTrajectory = this.trajectoryGenerator.generateBezierPath(
            this.trajectoryGenerator.getCurrentMousePosition(), targetCoords, 200 + Math.random() * 300
          );
          await this.trajectoryGenerator.simulateMouseTrajectory(finalTrajectory);
        }
        // 6. Execute final click event sequence
        await this.executeClickSequence(element, targetCoords);
        // 7. Update mouse position record
        this.trajectoryGenerator.setMousePosition(targetCoords.x, targetCoords.y);
        // 8. Occasional unconscious behavior
        if (Math.random() < 0.05) {
          setTimeout(() => { this.errorSimulator.simulateUnintentionalScroll(); }, 1000 + Math.random() * 3000);
        }
        return true;
      } catch (error) {
        this.logger.error(`Click simulation failed: ${error.message}`);
        return false;
      }
    }

    async executeClickSequence(element, coords) {
      const eventOptions = {
        bubbles: true, cancelable: true,
        clientX: coords.x, clientY: coords.y, button: 0
      };
      element.dispatchEvent(new MouseEvent('mousedown', eventOptions));
      const holdTime = this.humanBehavior.generateHumanDelay(40, 120, 'clicking');
      await this.sleep(holdTime);
      element.dispatchEvent(new MouseEvent('mouseup', eventOptions));
      element.dispatchEvent(new MouseEvent('click', eventOptions));
      this.lastClickTime = Date.now();
      validateClickCoordinate(coords, element.getBoundingClientRect());
    }

    async simulateHumanTyping(inputElement, text, previousMessage = '') {
      if (!inputElement || !text) return { success: false, attempts: 0, error: 'Missing element or text' };
      try {
        this.humanBehavior.setCurrentActivity('typing');
        const intelligentDelay = this.humanBehavior.cognitiveDelay.generateIntelligentInputDelay(text, previousMessage);
        await this.sleep(intelligentDelay.breakdown.reading);
        await this.sleep(intelligentDelay.breakdown.thinking);
        await this.sleep(intelligentDelay.breakdown.preparation);
        if (intelligentDelay.breakdown.hesitation > 0) {
          await this.sleep(intelligentDelay.breakdown.hesitation);
        }
        const maxRetries = 3;
        const useErrorSimulation = this.humanBehavior.behaviorPatterns &&
                                   this.humanBehavior.behaviorPatterns.accuracy < 0.95;
        for (let attempt = 1; attempt <= maxRetries; attempt++) {
          let inputSuccess = false;
          if (useErrorSimulation && Math.random() < 0.4) {
            inputSuccess = await this.errorSimulator.simulateTypingWithErrors(inputElement, text);
          } else {
            inputSuccess = await this.simulateIntelligentTypingStandard(inputElement, text);
          }
          if (inputSuccess) {
            return { success: true, attempts: attempt, error: null };
          }
          if (attempt < maxRetries) {
            const retryDelay = 500 + Math.random() * 500;
            await this.sleep(retryDelay);
            try {
              const isContentEditable = inputElement.contentEditable === 'true' ||
                                        inputElement.hasAttribute('contenteditable');
              if (isContentEditable) { inputElement.textContent = ''; inputElement.innerHTML = ''; }
              else { inputElement.value = ''; }
            } catch (clearError) { /* ignore */ }
          }
        }
        return { success: false, attempts: maxRetries, error: 'Input validation failed after max retries' };
      } catch (error) {
        return { success: false, attempts: 0, error: error.message };
      }
    }

    async simulateIntelligentTypingStandard(inputElement, text) {
      const isContentEditable = inputElement.contentEditable === 'true' ||
                                inputElement.hasAttribute('contenteditable');
      inputElement.focus();
      // Clear existing content
      if (isContentEditable) { inputElement.textContent = ''; inputElement.innerHTML = ''; }
      else { inputElement.value = ''; }
      // Type character by character
      let currentText = '';
      for (let i = 0; i < text.length; i++) {
        const char = text[i];
        currentText += char;
        inputElement.dispatchEvent(new KeyboardEvent('keydown', {
          key: char, code: `Key${char.toUpperCase()}`, bubbles: true, cancelable: true
        }));
        // Update element content
        if (isContentEditable) {
          inputElement.textContent = currentText;
          inputElement.innerHTML = currentText;
          inputElement.dispatchEvent(new Event('input', { bubbles: true, cancelable: true }));
          inputElement.dispatchEvent(new CustomEvent('input', {
            bubbles: true, cancelable: true, detail: { value: currentText }
          }));
          // Try triggering Vue reactivity
          try {
            const vueInstance = inputElement.__vue__ || inputElement._vueParentComponent;
            if (vueInstance && vueInstance.$forceUpdate) { vueInstance.$forceUpdate(); }
          } catch (vueError) { /* ignore */ }
        } else {
          inputElement.value = currentText;
          inputElement.dispatchEvent(new Event('input', { bubbles: true }));
        }
        inputElement.dispatchEvent(new KeyboardEvent('keyup', {
          key: char, code: `Key${char.toUpperCase()}`, bubbles: true, cancelable: true
        }));
        // Smart inter-character delay
        if (i < text.length - 1) {
          const nextChar = text[i + 1];
          const interCharDelay = this.humanBehavior.cognitiveDelay.calculateInterCharDelay(char, nextChar, i, text);
          await this.sleep(interCharDelay);
        }
      }
      // Final events to ensure state sync
      if (isContentEditable) {
        inputElement.blur(); await this.sleep(50); inputElement.focus();
        inputElement.dispatchEvent(new Event('change', { bubbles: true, cancelable: true }));
        inputElement.dispatchEvent(new FocusEvent('blur', { bubbles: true }));
        inputElement.dispatchEvent(new FocusEvent('focus', { bubbles: true }));
      } else {
        inputElement.dispatchEvent(new Event('change', { bubbles: true }));
      }
      return await this.validateInputContent(inputElement, text, isContentEditable);
    }

    sleep(ms) { return new Promise(resolve => setTimeout(resolve, ms)); }

    async validateInputContent(inputElement, expectedText, isContentEditable) {
      if (!inputElement || !expectedText) return false;
      const maxValidationAttempts = 3;
      const validationDelay = 100;
      for (let attempt = 1; attempt <= maxValidationAttempts; attempt++) {
        await this.sleep(validationDelay);
        const currentContent = this.getInputElementContent(inputElement, isContentEditable);
        const validationResults = this.performContentValidation(currentContent, expectedText);
        if (validationResults.isValid) return true;
        if (attempt === maxValidationAttempts) return false;
      }
      return false;
    }

    getInputElementContent(inputElement, isContentEditable) {
      try {
        if (isContentEditable) { return inputElement.textContent?.trim() || inputElement.innerHTML?.trim() || ''; }
        else { return inputElement.value?.trim() || ''; }
      } catch (error) { return ''; }
    }

    performContentValidation(actualContent, expectedContent) {
      const cleanActual = actualContent.trim();
      const cleanExpected = expectedContent.trim();
      if (cleanActual === cleanExpected) { return { isValid: true, strategy: 'exact match' }; }
      if (cleanActual.includes(cleanExpected)) { return { isValid: true, strategy: 'contains match' }; }
      if (cleanActual.length >= cleanExpected.length * 0.8) {
        const similarity = calculateSimilarity(cleanActual, cleanExpected);
        if (similarity >= 0.85) { return { isValid: true, strategy: `similarity (${(similarity*100).toFixed(1)}%)` }; }
      }
      const coreActual = cleanActual.replace(/[^\w\u4e00-\u9fff]/g, '');
      const coreExpected = cleanExpected.replace(/[^\w\u4e00-\u9fff]/g, '');
      if (coreActual === coreExpected && coreActual.length > 0) { return { isValid: true, strategy: 'core content match' }; }
      if (coreExpected.length > 0 && coreActual.includes(coreExpected.substring(0, Math.floor(coreExpected.length / 2)))) {
        const partialRatio = coreActual.length / coreExpected.length;
        if (partialRatio >= 0.5) { return { isValid: true, strategy: `partial match (${(partialRatio*100).toFixed(1)}%)` }; }
      }
      return { isValid: false, strategy: 'no match' };
    }

    calculateStringSimilarity(str1, str2) {
      const maxLength = Math.max(str1.length, str2.length);
      if (maxLength === 0) return 1.0;
      const distance = this.levenshteinDistance(str1, str2);
      return 1.0 - (distance / maxLength);
    }

    levenshteinDistance(str1, str2) {
      if (str1.length === 0) return str2.length;
      if (str2.length === 0) return str1.length;
      const matrix = [];
      for (let i = 0; i <= str2.length; i++) { matrix[i] = [i]; }
      for (let j = 0; j <= str1.length; j++) { matrix[0][j] = j; }
      for (let i = 1; i <= str2.length; i++) {
        for (let j = 1; j <= str1.length; j++) {
          if (str2.charAt(i - 1) === str1.charAt(j - 1)) { matrix[i][j] = matrix[i - 1][j - 1]; }
          else {
            matrix[i][j] = Math.min(matrix[i - 1][j - 1] + 1, matrix[i][j - 1] + 1, matrix[i - 1][j] + 1);
          }
        }
      }
      return matrix[str2.length][str1.length];
    }

    analyzeValidationFailure(actualContent, expectedContent, inputElement) {
      const analysis = [];
      if (actualContent.length === 0) { analysis.push('Input is empty - DOM events may not have triggered correctly'); }
      else if (actualContent.length < expectedContent.length / 2) { analysis.push('Content too short - input process may have been interrupted'); }
      if (actualContent.includes('undefined') || actualContent.includes('null')) { analysis.push('Content contains undefined/null - possible JS variable issue'); }
      if (inputElement.disabled) { analysis.push('Input element is disabled'); }
      if (inputElement.readOnly) { analysis.push('Input element is read-only'); }
      if (!inputElement.offsetParent) { analysis.push('Input element is not visible'); }
      if (document.activeElement !== inputElement) { analysis.push('Input element lost focus'); }
      if (analysis.length > 0) { this.logger.error(`Failure analysis: ${analysis.join('; ')}`); }
      else { this.logger.error('Failure analysis: no obvious issues detected'); }
    }
  }

  // ==================== index.js — factory + exports ====================

  const ConsoleLogger = {
    debug: (...args) => console.debug('[HumanSimulator:DEBUG]', ...args),
    info: (...args) => console.info('[HumanSimulator:INFO]', ...args),
    warn: (...args) => console.warn('[HumanSimulator:WARN]', ...args),
    error: (...args) => console.error('[HumanSimulator:ERROR]', ...args)
  };

  const SilentLogger = {
    debug: () => {}, info: () => {}, warn: () => {}, error: () => {}
  };

  class LocalStorageAdapter {
    async get(key, defaultValue) {
      try {
        const val = localStorage.getItem(`hbs_${key}`);
        return val ? JSON.parse(val) : defaultValue;
      } catch { return defaultValue; }
    }
    async set(key, value) {
      try { localStorage.setItem(`hbs_${key}`, JSON.stringify(value)); }
      catch { /* quota exceeded, silently fail */ }
    }
  }

  async function createHumanSimulator(options = {}) {
    const {
      verbose = false, logger = null, storage = null,
      persistProfile = true, initialProfile = null, errorRates = null
    } = options;
    const resolvedLogger = logger || (verbose ? ConsoleLogger : SilentLogger);
    const resolvedStorage = storage || (persistProfile ? new LocalStorageAdapter() : new MemoryStorage());
    const humanBehavior = await HumanBehaviorSimulator.create({
      logger: resolvedLogger, storage: resolvedStorage, initialProfile
    });
    const eventSimulator = new AntiStatisticEventSimulator(humanBehavior, resolvedLogger);
    if (errorRates) { eventSimulator.errorSimulator.setErrorRates(errorRates); }
    resolvedLogger.info('HumanBehaviorSimulator initialized');
    resolvedLogger.info(`  Behavior profile: ${humanBehavior.behaviorPatterns.type} (speed=${humanBehavior.behaviorPatterns.speed})`);
    return { humanBehavior, eventSimulator };
  }

  // ==================== Global export ====================
  window.HumanSimulator = {
    // Factory
    createHumanSimulator,

    // Core classes
    HumanBehaviorSimulator,
    AntiStatisticEventSimulator,

    // Sub-modules (for advanced custom assembly)
    PoissonTimingSimulator,
    CognitiveDelaySimulator,
    MouseTrajectoryGenerator,
    HumanErrorSimulator,

    // Storage implementations
    MemoryStorage,
    LocalStorageAdapter,

    // Loggers
    ConsoleLogger,
    SilentLogger,

    // Utility functions
    normalRandom,
    calculateDistance,
    cubicBezier,
    getPerpendicularVector,
    calculateSimilarity,
    validateClickCoordinate
  };
})();
