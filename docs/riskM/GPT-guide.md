# 请求总览
打开Chrome的Dev Tools，可以发现发起会话的主要请求为：https://chatgpt.com/backend-api/f/conversation（以前似乎为：https://chatgpt.com/backend-api/conversation），payload如下：

```
{
  "action": "next",
  "messages": [
    {
      "id": "a22491d1-7d0c-452a-96e3-916f2457b083",
      "author": {
        "role": "user"
      },
      "create_time": 1780661199.386,
      "content": {
        "content_type": "text",
        "parts": [
          "你好"
        ]
      },
      "metadata": {
        "developer_mode_connector_ids": [],
        "selected_sources": [],
        "selected_github_repos": [],
        "selected_all_github_repos": false,
        "serialization_metadata": {
          "custom_symbol_offsets": []
        }
      }
    }
  ],
  "parent_message_id": "client-created-root", // 新建会话的话是 client-created-root， 否则为上一条消息的id
  "model": "gpt-5-5-thinking", // 使用的 model，gpt-5-5, gpt-5-5-thinking, gpt-5-5-pro
  "client_prepare_state": "success",
  "timezone_offset_min": 420,
  "timezone": "America/Los_Angeles",
  "conversation_mode": {
    "kind": "primary_assistant"
  },
  "enable_message_followups": true,
  "system_hints": [],
  "supports_buffering": true,
  "supported_encodings": [
    "v1"
  ],
  "client_contextual_info": {
    "is_dark_mode": true,
    "time_since_loaded": 227,
    "page_height": 1214,
    "page_width": 1281,
    "pixel_ratio": 1,
    "screen_height": 1440,
    "screen_width": 2560,
    "app_name": "chatgpt.com"
  },
  "paragen_cot_summary_display_override": "allow",
  "force_parallel_switch": "auto",
  "thinking_effort": "extended" // 思考强度
  // "history_and_training_disabled": true 如果开的临时会话为true，chatgpt2api使用的就是临时会话作为代理
}
```

Header如下：

```
{
  "accept": "text/event-stream",
  "accept-language": "en-US,en;q=0.9",
  "authorization": "Bearer <REDACTED>",
  "content-type": "application/json",
  "oai-client-build-number": "7172950",
  "oai-client-version": "prod-760ab00c2c9dc7017b94fed36021fb0bb6c993e1",
  "oai-device-id": "f615ef5e-ac0f-4523-b9a1-9b22ac1cd1eb",
  "oai-echo-logs": "0,4800,1,6292,0,93092,1,93125,0,104336,1,105302,0,223641,1,233850,0,549255",
  "oai-language": "en-US",
  "oai-session-id": "a76a3c44-9b2f-4554-96e3-70348289773c",
  "oai-telemetry": "[1,234.19999998807907,9,29,7,2,0,237]",
  "openai-sentinel-chat-requirements-token": "<REDACTED>",
  "openai-sentinel-proof-token": "<REDACTED>",
  "openai-sentinel-turnstile-token": "<REDACTED>",
  "origin": "https://chatgpt.com",
  "priority": "u=1, I",
  "referer": "https://chatgpt.com/c/6a22bbcc-fd54-83e8-a23c-5f2db5642f14",
  "sec-ch-ua": "\"Google Chrome\";v=\"147\", \"Not.A/Brand\";v=\"8\", \"Chromium\";v=\"147\"",
  "sec-ch-ua-arch": "\"arm\"",
  "sec-ch-ua-bitness": "\"64\"",
  "sec-ch-ua-full-version": "\"147.0.7727.117\"",
  "sec-ch-ua-full-version-list": "\"Google Chrome\";v=\"147.0.7727.117\", \"Not.A/Brand\";v=\"8.0.0.0\", \"Chromium\";v=\"147.0.7727.117\"",
  "sec-ch-ua-mobile": "?0",
  "sec-ch-ua-model": "\"\"",
  "sec-ch-ua-platform": "\"macOS\"",
  "sec-ch-ua-platform-version": "\"11.7.6\"",
  "sec-fetch-dest": "empty",
  "sec-fetch-mode": "cors",
  "sec-fetch-site": "same-origin",
  "user-agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36",
  "x-conduit-token": "<REDACTED>",
  "x-oai-is": "<REDACTED>", // 实测没有什么影响
  "x-oai-turn-trace-id": "f1aaa5f3-d066-4ba2-8099-f3024df375e5",
  "x-openai-target-path": "/backend-api/f/conversation",
  "x-openai-target-route": "/backend-api/f/conversation",
  "cookie": "xxx"
}
```

可以发现其中有非常多的token，authorization中是访问的access token，剩下的还有openai-sentinel-chat-requirements-token、openai-sentinel-proof-token、openai-sentinel-turnstile-token、x-conduit-token

返回内容如下：

```
event: delta_encoding
data: "v1"

data: {"type": "resume_conversation_token", "kind": "topic", "token": "eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJjb25kdWl0X3V1aWQiOiI3NWQwYWM2ZWM1YTE0NjBhYWM0NjNkZDRlY2EyNmM1MyIsImNvbmR1aXRfbG9jYXRpb24iOiIxMC4xMjkuMTc1LjU3OjgzMDciLCJjbHVzdGVyIjoidW5pZmllZC0xNDkiLCJpYXQiOjE3ODA2NjE1MjQsImV4cCI6MTc4MDY2ODcyNCwidHVybl90b3BpY19pZCI6ImNvbnZlcnNhdGlvbi10dXJuLWI1N2M0YWRiLWFhMzctNGJmOS1hNzU0LTkzNWJkOTI4NzM2ZCJ9.7FRSs6QgR7b8mekYcEq4Lj3RR39_YijBu6Q-Ri9XbzgSVapgmORal5VYRLHAjcyML4Q9wMzL7AxZd8Sr3BWxZQ", "conversation_id": "6a22bbcc-fd54-83e8-a23c-5f2db5642f14"}

data: {"type": "stream_handoff", "conversation_id": "6a22bbcc-fd54-83e8-a23c-5f2db5642f14", "turn_exchange_id": "b57c4adb-aa37-4bf9-a754-935bd928736d", "options": [{"type": "resume_sse_endpoint", "topic_id": "conversation-turn-b57c4adb-aa37-4bf9-a754-935bd928736d"}, {"type": "subscribe_ws_topic", "topic_id": "conversation-turn-b57c4adb-aa37-4bf9-a754-935bd928736d"}]}

data: [DONE]
```


可以发现其中并没有返回的具体的文本内容，那文本内容在哪返回的呢？打开ws相关链接可以看到
猜测可能是为了省ws的连接数，选择了一个页面只开一个ws链接，所有的请求响应都走的同一个ws进行返回。当然ai响应的文本除了这种获取方式，还有一个接口为：https://chatgpt.com/backend-api/conversation/{conversation_id}，通过对话的id即可获取对话的内容

通过这个接口我们也可以发现，OpenAI其实把每部分都分的很开，而不是一个整体。


# openai-sentinel-chat-requirements-token

## p
OpenAI为了增加爬取成本，采用了一种 PoW 的工作机制，客户端需要用 PoW 来证明请求来自真实浏览器，也就是让客户端做一道"计算题"，答案附在请求里，服务端验证。由于计算需要时间，大规模爬取需要消耗服务器的计算资源，成本会显著上升。

首先需要生成p，post请求到https://chatgpt.com/backend-api/sentinel/chat-requirements/prepare，获取是否需要进行pow和turnstile验证。


p生成具体实现如下：

getRequirementsTokenBlocking()
  -> _generateRequirementsTokenAnswerBlocking()
      -> getConfig()
      -> e[3] = 1
      -> e[9] = performance.now() - t
      -> aR(e) // 把数组 JSON 序列化后 base64
  -> "gAAAAAC" + encodedResult

原始脚本如下：

```
_generateRequirementsTokenAnswerBlocking() {
    let e = `e`
      , t = performance.now();
    try {
        let e = this.getConfig();
        return e[3] = 1,
        e[9] = performance.now() - t,
        aR(e)
    } catch (t) {
        e = aR(String(t))
    }
    return this.errorPrefix + e
}
getConfig() {
    return [
        screen?.width + screen?.height,
        `` + new Date(),
        performance?.memory?.jsHeapSizeLimit,
        Math?.random(),
        navigator.userAgent,
        iR(
            Array.from(document.scripts)
            .map((e) => e?.src)
            .filter((e) => e),
        ),
        (Array.from(document.scripts || [])
            .map((e) => e?.src?.match(`c/[^/]*/_`))
            .filter((e) => e?.length)[0] ?? [])[0] ??
            document.documentElement.getAttribute(`data-build`),
        navigator.language,
        navigator.languages?.join(`,`),
        Math?.random(),
        Vvt(),
        iR(Object.keys(document)),
        iR(Object.keys(window)),
        performance.now(),
        this.sid,
        [...new URLSearchParams(window.location.search).keys()].join(`,`),
        navigator?.hardwareConcurrency,
        performance.timeOrigin,
        Number(`ai` in window),
        Number(`createPRNG` in window),
        Number(`cache` in window),
        Number(`data` in window),
        Number(`solana` in window),
        Number(`dump` in window),
        Number(`InstallTrigger` in window),
        ];
}
```

获取到的结果如下：

![alt text](image.png)

分析了一下：

getConfig() 里收集的数组按下标大概是：
下标	内容
0	screen.width + screen.height，屏幕宽高相加
1	String(new Date)，当前时间字符串
2	performance.memory.jsHeapSizeLimit，JS 堆内存限制
3	Math.random()，但 blocking 版本后面会改成 1
4	navigator.userAgent
5	从 document.scripts 里随机挑一个脚本 src
6	从脚本 URL 中匹配 c/[^/]*/_，否则取 document.documentElement.getAttribute("data-build")
7	navigator.language
8	navigator.languages?.join(",")
9	Math.random()，但 blocking 版本后面会改成生成耗时
10	Vvt()，随机取一个 navigator 原型属性，并尝试读出它的值
11	iR(Object.keys(document))，随机取一个 document key
12	iR(Object.keys(window))，随机取一个 window key
13	performance.now()
14	this.sid，构造函数里生成的随机 session id
15	当前 URL query 参数名列表，用逗号拼接
16	navigator.hardwareConcurrency，CPU 逻辑核心数
17	performance.timeOrigin
18	Number("ai" in window)
19	Number("createPRNG" in window)
20	Number("cache" in window)
21	Number("data" in window)
22	Number("solana" in window)
23	Number("dump" in window)
24	Number("InstallTrigger" in window)，常用于 Firefox 环境检测

```
import time, random, base64, json, uuid
from datetime import datetime, timezone, timedelta

def _legacy_parse_time() -> str:
    now = datetime.now(timezone(timedelta(hours=-5)))
    return now.strftime("%a %b %d %Y %H:%M:%S") + " GMT-0500 (Eastern Standard Time)"

t = time.perf_counter()
navigator_key = random.choice([
    "registerProtocolHandler−function registerProtocolHandler() { [native code] }",
    "storage−[object StorageManager]",
    "locks−[object LockManager]",
    "appCodeName−Mozilla",
    "permissions−[object Permissions]",
    "share−function share() { [native code] }",
    "webdriver−false",
    "managed−[object NavigatorManagedData]",
    "canShare−function canShare() { [native code] }",
    "vendor−Google Inc.",
    "mediaDevices−[object MediaDevices]",
    "vibrate−function vibrate() { [native code] }",
    "storageBuckets−[object StorageBucketManager]",
    "mediaCapabilities−[object MediaCapabilities]",
    "cookieEnabled−true",
    "virtualKeyboard−[object VirtualKeyboard]",
    "product−Gecko",
    "presentation−[object Presentation]",
    "onLine−true",
    "mimeTypes−[object MimeTypeArray]",
    "credentials−[object CredentialsContainer]",
    "serviceWorker−[object ServiceWorkerContainer]",
    "keyboard−[object Keyboard]",
    "gpu−[object GPU]",
    "doNotTrack",
    "serial−[object Serial]",
    "pdfViewerEnabled−true",
    "language−zh-CN",
    "geolocation−[object Geolocation]",
    "userAgentData−[object NavigatorUAData]",
    "getUserMedia−function getUserMedia() { [native code] }",
    "sendBeacon−function sendBeacon() { [native code] }",
    "hardwareConcurrency−32",
    "windowControlsOverlay−[object WindowControlsOverlay]",
])
window_key = random.choice([
    "0", "window", "self", "document", "name", "location",
    "customElements", "history", "navigation", "innerWidth", "innerHeight",
    "scrollX", "scrollY", "visualViewport", "screenX", "screenY",
    "outerWidth", "outerHeight", "devicePixelRatio", "screen", "chrome",
    "navigator", "onresize", "performance", "crypto", "indexedDB",
    "sessionStorage", "localStorage", "scheduler", "alert", "atob", "btoa",
    "fetch", "matchMedia", "postMessage", "queueMicrotask",
    "requestAnimationFrame", "setInterval", "setTimeout", "caches",
    "__NEXT_DATA__", "__BUILD_MANIFEST", "__NEXT_PRELOADREADY",
])
DOCUMENT_KEYS = ['__reactContainer$fzelfjyxej8', '_reactListening5dehydibo78', "location"]
CORES = [8, 16, 24, 32]
user_agent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"
data_build = "prod-760ab00c2c9dc7017b94fed36021fb0bb6c993e1"
script_source = None
screen = [[1920, 1080], [1440, 900], [2560, 1440], [3840, 2160]]
pow_config = [
    sum(random.choices(screen, k=1)[0]),
    _legacy_parse_time(),
    4294705152, # 约等于 4 * 1024 * 1024 * 1024
    random.random(),
    user_agent,
    script_source,
    data_build,
    "en-US",
    "en-US,es-US,en,es",
    random.random(),
    navigator_key,
    random.choice(DOCUMENT_KEYS),
    window_key,
    time.perf_counter() * 1000,
    str(uuid.uuid4()),
    "",
    random.choice(CORES),
    time.time() * 1000 - (time.perf_counter() * 1000),
    0,
    0,
    0,
    0,
    0,
    0,
    0 # if firefox then 1, if edge/chrome then 0
]
pow_config[3] = 1
pow_config[9] = (time.perf_counter() - t) * 1000
p_token = "gAAAAAC" + base64.b64encode(json.dumps(pow_config, separators=(",", ":"), ensure_ascii=False).encode()).decode()
print(p_token)
```

## pow（openai-sentinel-proof-token）
相关原始代码：
```
_getAnswer(e, t = false) {
    let n = `gAAAAAB`;

    if (!e?.proofofwork?.required)
        return null;

    let { seed: r, difficulty: i } = e.proofofwork;

    if (!(typeof r == `string` && typeof i == `string`))
        return null;

    let a = this.answers.get(r);
    if (typeof a == `string`)
        return a;

    if (t) {
        let e = n + this._generateAnswerSync(r, i);
        return this.answers.set(r, e), e;
    }

    return this.answers.has(r) ||
        this.answers.set(r, this._generateAnswerAsync(r, i)),
        Promise.resolve()
            .then(async () => n + await this.answers.get(r))
            .then(e => (this.answers.set(r, e), e))
}

_generateAnswerSync(seed, difficulty) {
    let start = performance.now();

    try {
        let config = this.getConfig();

        for (let i = 0; i < this.maxAttempts; i++) {
            let answer = this._runCheck(start, seed, difficulty, config, i);
            if (answer)
                return answer;
        }
    } catch (e) {
        return this.buildGenerateFailMessage(e);
    }

    return this.buildGenerateFailMessage();
}

async _generateAnswerAsync(seed, difficulty) {
    let start = performance.now();

    try {
        let idle = null;
        let config = this.getConfig();

        for (let i = 0; i < this.maxAttempts; i++) {
            (!idle || idle.timeRemaining() <= 0) && (idle = await Hvt());

            let answer = this._runCheck(start, seed, difficulty, config, i);
            if (answer)
                return answer;
        }
    } catch (e) {
        return this.buildGenerateFailMessage(e);
    }

    return this.buildGenerateFailMessage();
}

_runCheck = (start, seed, difficulty, config, attempt) => {
    config[3] = attempt;
    config[9] = Math.round(performance.now() - start);

    let answer = aR(config);

    return zvt(seed + answer).substring(0, difficulty.length) <= difficulty
        ? answer + `~S`
        : null;
}

function zvt(e) {
    let t = 2166136261;
    for (let n = 0; n < e.length; n++)
        t ^= e.charCodeAt(n),
        t = Math.imul(t, 16777619) >>> 0;
    return t ^= t >>> 16,
    t = Math.imul(t, 2246822507) >>> 0,
    t ^= t >>> 13,
    t = Math.imul(t, 3266489909) >>> 0,
    t ^= t >>> 16,
    (t >>> 0).toString(16).padStart(8, `0`)
}
```

python实现：

```
# ---------------------------------------------------------------------------
# proof-of-work
# ---------------------------------------------------------------------------

def _legacy_parse_time() -> str:
    now = datetime.now(timezone(timedelta(hours=-5)))
    return now.strftime("%a %b %d %Y %H:%M:%S") + " GMT-0500 (Eastern Standard Time)"

def _build_pow_config(
    user_agent: str,
    script_sources: Sequence[str] | None = None,
    data_build: str = "",
) -> list[Any]:
    navigator_key = random.choice([
        "registerProtocolHandler−function registerProtocolHandler() { [native code] }",
        "storage−[object StorageManager]",
        "locks−[object LockManager]",
        "appCodeName−Mozilla",
        "permissions−[object Permissions]",
        "share−function share() { [native code] }",
        "webdriver−false",
        "managed−[object NavigatorManagedData]",
        "canShare−function canShare() { [native code] }",
        "vendor−Google Inc.",
        "mediaDevices−[object MediaDevices]",
        "vibrate−function vibrate() { [native code] }",
        "storageBuckets−[object StorageBucketManager]",
        "mediaCapabilities−[object MediaCapabilities]",
        "cookieEnabled−true",
        "virtualKeyboard−[object VirtualKeyboard]",
        "product−Gecko",
        "presentation−[object Presentation]",
        "onLine−true",
        "mimeTypes−[object MimeTypeArray]",
        "credentials−[object CredentialsContainer]",
        "serviceWorker−[object ServiceWorkerContainer]",
        "keyboard−[object Keyboard]",
        "gpu−[object GPU]",
        "doNotTrack",
        "serial−[object Serial]",
        "pdfViewerEnabled−true",
        "language−zh-CN",
        "geolocation−[object Geolocation]",
        "userAgentData−[object NavigatorUAData]",
        "getUserMedia−function getUserMedia() { [native code] }",
        "sendBeacon−function sendBeacon() { [native code] }",
        "hardwareConcurrency−32",
        "windowControlsOverlay−[object WindowControlsOverlay]",
    ])
    window_key = random.choice([
        "0", "window", "self", "document", "name", "location",
        "customElements", "history", "navigation", "innerWidth", "innerHeight",
        "scrollX", "scrollY", "visualViewport", "screenX", "screenY",
        "outerWidth", "outerHeight", "devicePixelRatio", "screen", "chrome",
        "navigator", "onresize", "performance", "crypto", "indexedDB",
        "sessionStorage", "localStorage", "scheduler", "alert", "atob", "btoa",
        "fetch", "matchMedia", "postMessage", "queueMicrotask",
        "requestAnimationFrame", "setInterval", "setTimeout", "caches",
        "__NEXT_DATA__", "__BUILD_MANIFEST", "__NEXT_PRELOADREADY",
    ])
    DOCUMENT_KEYS = ['__reactContainer$fzelfjyxej8', '_reactListening5dehydibo78', "location"]
    CORES = [8, 16, 24, 32]
    script_source = random.choice(list(script_sources)) if script_sources else None
    screen = [[1920, 1080], [1440, 900], [2560, 1440], [3840, 2160]]
    return [
        sum(random.choices(screen, k=1)[0]),
        _legacy_parse_time(),
        4294705152, # almost equal 4 * 1024 * 1024 * 1024
        1,
        user_agent,
        script_source,
        data_build,
        "en-US",
        "en-US,es-US,en,es",
        random.random(),
        navigator_key,
        random.choice(DOCUMENT_KEYS),
        window_key,
        time.perf_counter() * 1000,
        _new_uuid(),
        "",
        random.choice(CORES),
        time.time() * 1000 - (time.perf_counter() * 1000),
        0,
        0,
        0,
        0,
        0,
        0,
        0 # if firefox then 1, if edge/chrome then 0
    ]

def _pow_generate(seed: str, difficulty: str, config: list[Any], limit: int = 500000) -> tuple[str, bool]:
    target = bytes.fromhex(difficulty)
    diff_len = len(difficulty) // 2
    seed_bytes = seed.encode()
    static_1 = (json.dumps(config[:3], separators=(",", ":"), ensure_ascii=False)[:-1] + ",").encode()
    static_2 = ("," + json.dumps(config[4:9], separators=(",", ":"), ensure_ascii=False)[1:-1] + ",").encode()
    static_3 = ("," + json.dumps(config[10:], separators=(",", ":"), ensure_ascii=False)[1:]).encode()
    for i in range(limit):
        final_json = static_1 + str(i).encode() + static_2 + str(i >> 1).encode() + static_3
        encoded = base64.b64encode(final_json)
        digest = hashlib.sha3_512(seed_bytes + encoded).digest()
        if digest[:diff_len] <= target:
            return encoded.decode(), True
    fallback = "wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D" + base64.b64encode(f'"{seed}"'.encode()).decode()
    return fallback, False

def _build_legacy_requirements_token(
    user_agent: str,
    script_sources: Sequence[str] | None = None,
    data_build: str = "",
) -> str:
    # seed = format(random.random())
    config = _build_pow_config(user_agent, script_sources=script_sources, data_build=data_build)
    # answer, _ = _pow_generate(seed, "0fffff", config)
    return "gAAAAAC" + base64.b64encode(json.dumps(config, separators=(",", ":"), ensure_ascii=False).encode()).decode()

def _build_proof_token(
    seed: str,
    difficulty: str,
    user_agent: str,
    script_sources: Sequence[str] | None = None,
    data_build: str = "",
) -> str:
    config = _build_pow_config(user_agent, script_sources=script_sources, data_build=data_build)
    answer, solved = _pow_generate(seed, difficulty, config)
    if not solved:
        raise RuntimeError(f"failed to solve proof token: difficulty={difficulty}")
    return "gAAAAAB" + answer
```

## turnstile（openai-sentinel-turnstile-token）
obt(e, dx) 是一个 dx 指令执行器：它用当前 requirements token/prepare token 派生出的 key 解码 dx，把解码结果解析成 JSON 指令队列，然后在一个 Map-based 小 VM 里执行，最终通过 fbt 成功回调返回 base64 token；超时则返回执行步数。

相关代码：
```
async getEnforcementToken(e) {
	   if (e.turnstile?.dx) {
		    let dx = e.turnstile.dx;
		    let reqToken = uyt(e);
		    let cacheKey = reqToken ? `${reqToken}::${dx}` : dx;
		
		    let cachedPromise = this.dxPromiseCache.get(cacheKey);
		    if (cachedPromise)
		        return cachedPromise;
		
		    let p = obt(e, dx)
		        .then(token => (
		            token && this.enforcementTokenCache.set(e, token),
		            token
		        ))
		        .finally(() => {
		            this.dxPromiseCache.delete(cacheKey);
		        });
		
		    this.dxPromiseCache.set(cacheKey, p);
		    return p;
		}
		// 没有dx的情况
    return this.startEnforcement(e).then(e => (
        this.enforcementTokenPromise = null,
        e ? e.toString() : null
    ));
}

function obt(e, t) {
    return ibt(() => new Promise((n, r) => {
        let i = uyt(e ?? {}) ?? ``;

        cbt(),
        SR = 0,
        $.set(xR, i);

        let a = !1;

        setTimeout(() => {
            a = !0,
            n(`` + SR)
        }, 500),

        $.set(fbt, e => {
            a || (
                a = !0,
                n(btoa(`` + e))
            )
        }),

        $.set(pbt, e => {
            a || (
                a = !0,
                r(btoa(`` + e))
            )
        }),

        $.set(Lbt, (e, t, n, r) => {
            let i = Array.isArray(r),
                o = i ? n : [],
                s = (i ? r : n) || [];

            $.set(e, (...e) => {
                if (a)
                    return;

                let n = [...$.get(bR)];

                if (i)
                    for (let t = 0; t < o.length; t++) {
                        let n = o[t],
                            r = e[t];
                        $.set(n, r)
                    }

                return $.set(bR, [...s]),
                yR()
                    .then(() => $.get(t))
                    .catch(e => `` + e)
                    .finally(() => {
                        $.set(bR, n)
                    })
            })
        });

        try {
            $.set(bR, JSON.parse(sbt(atob(t), `` + $.get(xR)))),
            yR().catch(e => {
                n(btoa(SR + `: ` + e))
            })
        } catch (e) {
            n(btoa(SR + `: ` + e))
        }
    }))
}

function sbt(e, t) {
    let n = ``;
    for (let r = 0; r < e.length; r++)
        n += String.fromCharCode(
            e.charCodeAt(r) ^ t.charCodeAt(r % t.length)
        );
    return n
}

function ibt(e) {
    let t = Vbt.then(e, e);
    return Vbt = t.then(() => void 0, () => void 0),
    t
}
```

下面贴两个实现:
```
# ---------------------------------------------------------------------------
# turnstile solver
# ---------------------------------------------------------------------------

class _OrderedMap:
    def __init__(self) -> None:
        self.keys: list[str] = []
        self.values: dict[str, Any] = {}

    def add(self, key: str, value: Any) -> None:
        if key not in self.values:
            self.keys.append(key)
        self.values[key] = value

def _turnstile_to_str(value: Any) -> str:
    if value is None:
        return "undefined"
    if isinstance(value, float):
        return str(value)
    if isinstance(value, str):
        special = {
            "window.Math": "[object Math]",
            "window.Reflect": "[object Reflect]",
            "window.performance": "[object Performance]",
            "window.localStorage": "[object Storage]",
            "window.Object": "function Object() { [native code] }",
            "window.Reflect.set": "function set() { [native code] }",
            "window.performance.now": "function () { [native code] }",
            "window.Object.create": "function create() { [native code] }",
            "window.Object.keys": "function keys() { [native code] }",
            "window.Math.random": "function random() { [native code] }",
        }
        return special.get(value, value)
    if isinstance(value, list) and all(isinstance(item, str) for item in value):
        return ",".join(value)
    return str(value)

def _xor_string(text: str, key: str) -> str:
    if not key:
        return text
    return "".join(chr(ord(ch) ^ ord(key[i % len(key)])) for i, ch in enumerate(text))

def _solve_turnstile_token(dx: str, p: str) -> Optional[str]:
    try:
        decoded = base64.b64decode(dx).decode()
        token_list = json.loads(_xor_string(decoded, p))
    except Exception:
        return None

    process_map: Dict[Any, Any] = {}
    start_time = time.time()
    result = ""

    def func_1(e: float, t: float) -> None:
        process_map[e] = _xor_string(_turnstile_to_str(process_map[e]), _turnstile_to_str(process_map[t]))

    def func_2(e: float, t: Any) -> None:
        process_map[e] = t

    def func_3(e: str) -> None:
        nonlocal result
        result = base64.b64encode(e.encode()).decode()

    def func_5(e: float, t: float) -> None:
        current = process_map[e]
        incoming = process_map[t]
        if isinstance(current, (list, tuple)):
            process_map[e] = list(current) + [incoming]
            return
        if isinstance(current, (str, float)) or isinstance(incoming, (str, float)):
            process_map[e] = _turnstile_to_str(current) + _turnstile_to_str(incoming)
            return
        process_map[e] = "NaN"

    def func_6(e: float, t: float, n: float) -> None:
        tv = process_map[t]
        nv = process_map[n]
        if isinstance(tv, str) and isinstance(nv, str):
            value = f"{tv}.{nv}"
            process_map[e] = "https://chatgpt.com/" if value == "window.document.location" else value

    def func_7(e: float, *args: float) -> None:
        target = process_map[e]
        values = [process_map[arg] for arg in args]
        if isinstance(target, str) and target == "window.Reflect.set":
            obj, key_name, val = values
            obj.add(str(key_name), val)
        elif callable(target):
            target(*values)

    def func_8(e: float, t: float) -> None:
        process_map[e] = process_map[t]

    def func_14(e: float, t: float) -> None:
        process_map[e] = json.loads(process_map[t])

    def func_15(e: float, t: float) -> None:
        process_map[e] = json.dumps(process_map[t])

    def func_17(e: float, t: float, *args: float) -> None:
        call_args = [process_map[arg] for arg in args]
        target = process_map[t]
        if target == "window.performance.now":
            elapsed_ns = time.time_ns() - int(start_time * 1e9)
            process_map[e] = (elapsed_ns + random.random()) / 1e6
        elif target == "window.Object.create":
            process_map[e] = _OrderedMap()
        elif target == "window.Object.keys":
            if call_args and call_args[0] == "window.localStorage":
                process_map[e] = [
                    "STATSIG_LOCAL_STORAGE_INTERNAL_STORE_V4",
                    "STATSIG_LOCAL_STORAGE_STABLE_ID",
                    "client-correlated-secret",
                    "oai/apps/capExpiresAt",
                    "oai-did",
                    "STATSIG_LOCAL_STORAGE_LOGGING_REQUEST",
                    "UiState.isNavigationCollapsed.1",
                ]
        elif target == "window.Math.random":
            process_map[e] = random.random()
        elif callable(target):
            process_map[e] = target(*call_args)

    def func_18(e: float) -> None:
        process_map[e] = base64.b64decode(_turnstile_to_str(process_map[e])).decode()

    def func_19(e: float) -> None:
        process_map[e] = base64.b64encode(_turnstile_to_str(process_map[e]).encode()).decode()

    def func_20(e: float, t: float, n: float, *args: float) -> None:
        if process_map[e] == process_map[t]:
            target = process_map[n]
            if callable(target):
                target(*[process_map[arg] for arg in args])

    def func_21(*_: Any) -> None:
        return

    def func_23(e: float, t: float, *args: float) -> None:
        if process_map[e] is not None and callable(process_map[t]):
            process_map[t](*args)

    def func_24(e: float, t: float, n: float) -> None:
        tv = process_map[t]
        nv = process_map[n]
        if isinstance(tv, str) and isinstance(nv, str):
            process_map[e] = f"{tv}.{nv}"

    process_map.update({
        1: func_1, 2: func_2, 3: func_3, 5: func_5, 6: func_6,
        7: func_7, 8: func_8, 9: token_list, 10: "window",
        14: func_14, 15: func_15, 16: p, 17: func_17,
        18: func_18, 19: func_19, 20: func_20, 21: func_21,
        23: func_23, 24: func_24,
    })

    for token in token_list:
        try:
            fn = process_map.get(token[0])
            if callable(fn):
                fn(*token[1:])
        except Exception:
            continue
    return result or None
```

## openai-sentinel-chat-requirements-token
最后带着prepare_token、proofofwork、turnstile，请求https://chatgpt.com/backend-api/sentinel/chat-requirements/finalize，即可获得openai-sentinel-chat-requirements-token

## x-conduit-token
x-conduit-token由请求 POST https://chatgpt.com/backend-api/f/conversation/finalize后获得，payload和conversation一样，像是请求的一个预热。

## conversation
有了这些token之后，就可以开始请求conversation进行聊天了。

# 完整聊天代码
```
"""
standalone: ChatGPT CLI — generates sentinel tokens and holds an interactive conversation

Dependencies (pip): curl_cffi

Usage:
    python get_sentinel_token.py
    python get_sentinel_token.py --access-token <token> --model auto
"""

import argparse
import base64
import hashlib
import json
import random
import re
import time
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from html.parser import HTMLParser
from typing import Any, Dict, Iterator, Optional, Sequence

import curl_cffi.requests as requests

# ---------------------------------------------------------------------------
# constants
# ---------------------------------------------------------------------------

BASE_URL = "https://chatgpt.com"
DEFAULT_POW_SCRIPT = f"{BASE_URL}/backend-api/sentinel/sdk.js"
CLIENT_VERSION = "prod-a194cd50d4416d3c0b47c740f206b12ce60f5887"
CLIENT_BUILD_NUMBER = "6708908"

USER_AGENT = (
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0"
)
SEC_CH_UA = '"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"'

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

def _new_uuid() -> str:
    return str(uuid.uuid4())

# ---------------------------------------------------------------------------
# HTML bootstrap parser — extract script src list and data-build
# ---------------------------------------------------------------------------

class _ScriptSrcParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__()
        self.script_sources: list[str] = []
        self.data_build = ""

    def handle_starttag(self, tag: str, attrs: list[tuple[str, Optional[str]]]) -> None:
        if tag != "script":
            return
        attrs_dict = dict(attrs)
        src = attrs_dict.get("src")
        if not src:
            return
        self.script_sources.append(src)
        match = re.search(r"c/[^/]*/_", src)
        if match:
            self.data_build = match.group(0)

def _parse_pow_resources(html: str) -> tuple[list[str], str]:
    parser = _ScriptSrcParser()
    parser.feed(html)
    sources = parser.script_sources or [DEFAULT_POW_SCRIPT]
    data_build = parser.data_build
    if not data_build:
        m = re.search(r'<html[^>]*data-build="([^"]*)"', html)
        if m:
            data_build = m.group(1)
    return sources, data_build

# ---------------------------------------------------------------------------
# proof-of-work
# ---------------------------------------------------------------------------

def _legacy_parse_time() -> str:
    now = datetime.now(timezone(timedelta(hours=-5)))
    return now.strftime("%a %b %d %Y %H:%M:%S") + " GMT-0500 (Eastern Standard Time)"

def _build_pow_config(
    user_agent: str,
    script_sources: Optional[Sequence[str]] = None,
    data_build: str = "",
) -> list[Any]:
    navigator_key = random.choice([
        "registerProtocolHandler−function registerProtocolHandler() { [native code] }",
        "storage−[object StorageManager]",
        "locks−[object LockManager]",
        "appCodeName−Mozilla",
        "permissions−[object Permissions]",
        "share−function share() { [native code] }",
        "webdriver−false",
        "managed−[object NavigatorManagedData]",
        "canShare−function canShare() { [native code] }",
        "vendor−Google Inc.",
        "mediaDevices−[object MediaDevices]",
        "vibrate−function vibrate() { [native code] }",
        "storageBuckets−[object StorageBucketManager]",
        "mediaCapabilities−[object MediaCapabilities]",
        "cookieEnabled−true",
        "virtualKeyboard−[object VirtualKeyboard]",
        "product−Gecko",
        "presentation−[object Presentation]",
        "onLine−true",
        "mimeTypes−[object MimeTypeArray]",
        "credentials−[object CredentialsContainer]",
        "serviceWorker−[object ServiceWorkerContainer]",
        "keyboard−[object Keyboard]",
        "gpu−[object GPU]",
        "doNotTrack",
        "serial−[object Serial]",
        "pdfViewerEnabled−true",
        "language−zh-CN",
        "geolocation−[object Geolocation]",
        "userAgentData−[object NavigatorUAData]",
        "getUserMedia−function getUserMedia() { [native code] }",
        "sendBeacon−function sendBeacon() { [native code] }",
        "hardwareConcurrency−32",
        "windowControlsOverlay−[object WindowControlsOverlay]",
    ])
    window_key = random.choice([
        "0", "window", "self", "document", "name", "location",
        "customElements", "history", "navigation", "innerWidth", "innerHeight",
        "scrollX", "scrollY", "visualViewport", "screenX", "screenY",
        "outerWidth", "outerHeight", "devicePixelRatio", "screen", "chrome",
        "navigator", "onresize", "performance", "crypto", "indexedDB",
        "sessionStorage", "localStorage", "scheduler", "alert", "atob", "btoa",
        "fetch", "matchMedia", "postMessage", "queueMicrotask",
        "requestAnimationFrame", "setInterval", "setTimeout", "caches",
        "__NEXT_DATA__", "__BUILD_MANIFEST", "__NEXT_PRELOADREADY",
    ])
    document_keys = ["__reactContainer$fzelfjyxej8", "_reactListening5dehydibo78", "location"]
    cores = [8, 16, 24, 32]
    script_source = random.choice(list(script_sources)) if script_sources else None
    screen = [[1920, 1080], [1440, 900], [2560, 1440], [3840, 2160]]
    return [
        sum(random.choices(screen, k=1)[0]),
        _legacy_parse_time(),
        4294705152,
        1,
        user_agent,
        script_source,
        data_build,
        "en-US",
        "en-US,es-US,en,es",
        random.random(),
        navigator_key,
        random.choice(document_keys),
        window_key,
        time.perf_counter() * 1000,
        _new_uuid(),
        "",
        random.choice(cores),
        time.time() * 1000 - (time.perf_counter() * 1000),
        0, 0, 0, 0, 0, 0,
        0,  # 0 = edge/chrome, 1 = firefox
    ]

def _pow_generate(seed: str, difficulty: str, config: list[Any], limit: int = 500000) -> tuple[str, bool]:
    target = bytes.fromhex(difficulty)
    diff_len = len(difficulty) // 2
    seed_bytes = seed.encode()
    static_1 = (json.dumps(config[:3], separators=(",", ":"), ensure_ascii=False)[:-1] + ",").encode()
    static_2 = ("," + json.dumps(config[4:9], separators=(",", ":"), ensure_ascii=False)[1:-1] + ",").encode()
    static_3 = ("," + json.dumps(config[10:], separators=(",", ":"), ensure_ascii=False)[1:]).encode()
    for i in range(limit):
        final_json = static_1 + str(i).encode() + static_2 + str(i >> 1).encode() + static_3
        encoded = base64.b64encode(final_json)
        digest = hashlib.sha3_512(seed_bytes + encoded).digest()
        if digest[:diff_len] <= target:
            return encoded.decode(), True
    fallback = "wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D" + base64.b64encode(f'"{seed}"'.encode()).decode()
    return fallback, False

def _build_legacy_requirements_token(
    user_agent: str,
    script_sources: Optional[Sequence[str]] = None,
    data_build: str = "",
) -> str:
    config = _build_pow_config(user_agent, script_sources=script_sources, data_build=data_build)
    return "gAAAAAC" + base64.b64encode(
        json.dumps(config, separators=(",", ":"), ensure_ascii=False).encode()
    ).decode()

def _build_proof_token(
    seed: str,
    difficulty: str,
    user_agent: str,
    script_sources: Optional[Sequence[str]] = None,
    data_build: str = "",
) -> str:
    config = _build_pow_config(user_agent, script_sources=script_sources, data_build=data_build)
    answer, solved = _pow_generate(seed, difficulty, config)
    if not solved:
        raise RuntimeError(f"failed to solve proof token: difficulty={difficulty}")
    return "gAAAAAB" + answer

# ---------------------------------------------------------------------------
# turnstile solver
# ---------------------------------------------------------------------------

class _OrderedMap:
    def __init__(self) -> None:
        self.keys: list[str] = []
        self.values: Dict[str, Any] = {}

    def add(self, key: str, value: Any) -> None:
        if key not in self.values:
            self.keys.append(key)
        self.values[key] = value

def _turnstile_to_str(value: Any) -> str:
    if value is None:
        return "undefined"
    if isinstance(value, float):
        return str(value)
    if isinstance(value, str):
        special = {
            "window.Math": "[object Math]",
            "window.Reflect": "[object Reflect]",
            "window.performance": "[object Performance]",
            "window.localStorage": "[object Storage]",
            "window.Object": "function Object() { [native code] }",
            "window.Reflect.set": "function set() { [native code] }",
            "window.performance.now": "function () { [native code] }",
            "window.Object.create": "function create() { [native code] }",
            "window.Object.keys": "function keys() { [native code] }",
            "window.Math.random": "function random() { [native code] }",
        }
        return special.get(value, value)
    if isinstance(value, list) and all(isinstance(item, str) for item in value):
        return ",".join(value)
    return str(value)

def _xor_string(text: str, key: str) -> str:
    if not key:
        return text
    return "".join(chr(ord(ch) ^ ord(key[i % len(key)])) for i, ch in enumerate(text))

def _solve_turnstile_token(dx: str, p: str) -> Optional[str]:
    try:
        decoded = base64.b64decode(dx).decode()
        token_list = json.loads(_xor_string(decoded, p))
    except Exception:
        return None

    process_map: Dict[Any, Any] = {}
    start_time = time.time()
    result = ""

    def func_1(e: float, t: float) -> None:
        process_map[e] = _xor_string(_turnstile_to_str(process_map[e]), _turnstile_to_str(process_map[t]))

    def func_2(e: float, t: Any) -> None:
        process_map[e] = t

    def func_3(e: str) -> None:
        nonlocal result
        result = base64.b64encode(e.encode()).decode()

    def func_5(e: float, t: float) -> None:
        current = process_map[e]
        incoming = process_map[t]
        if isinstance(current, (list, tuple)):
            process_map[e] = list(current) + [incoming]
            return
        if isinstance(current, (str, float)) or isinstance(incoming, (str, float)):
            process_map[e] = _turnstile_to_str(current) + _turnstile_to_str(incoming)
            return
        process_map[e] = "NaN"

    def func_6(e: float, t: float, n: float) -> None:
        tv = process_map[t]
        nv = process_map[n]
        if isinstance(tv, str) and isinstance(nv, str):
            value = f"{tv}.{nv}"
            process_map[e] = "https://chatgpt.com/" if value == "window.document.location" else value

    def func_7(e: float, *args: float) -> None:
        target = process_map[e]
        values = [process_map[arg] for arg in args]
        if isinstance(target, str) and target == "window.Reflect.set":
            obj, key_name, val = values
            obj.add(str(key_name), val)
        elif callable(target):
            target(*values)

    def func_8(e: float, t: float) -> None:
        process_map[e] = process_map[t]

    def func_14(e: float, t: float) -> None:
        process_map[e] = json.loads(process_map[t])

    def func_15(e: float, t: float) -> None:
        process_map[e] = json.dumps(process_map[t])

    def func_17(e: float, t: float, *args: float) -> None:
        call_args = [process_map[arg] for arg in args]
        target = process_map[t]
        if target == "window.performance.now":
            elapsed_ns = time.time_ns() - int(start_time * 1e9)
            process_map[e] = (elapsed_ns + random.random()) / 1e6
        elif target == "window.Object.create":
            process_map[e] = _OrderedMap()
        elif target == "window.Object.keys":
            if call_args and call_args[0] == "window.localStorage":
                process_map[e] = [
                    "STATSIG_LOCAL_STORAGE_INTERNAL_STORE_V4",
                    "STATSIG_LOCAL_STORAGE_STABLE_ID",
                    "client-correlated-secret",
                    "oai/apps/capExpiresAt",
                    "oai-did",
                    "STATSIG_LOCAL_STORAGE_LOGGING_REQUEST",
                    "UiState.isNavigationCollapsed.1",
                ]
        elif target == "window.Math.random":
            process_map[e] = random.random()
        elif callable(target):
            process_map[e] = target(*call_args)

    def func_18(e: float) -> None:
        process_map[e] = base64.b64decode(_turnstile_to_str(process_map[e])).decode()

    def func_19(e: float) -> None:
        process_map[e] = base64.b64encode(_turnstile_to_str(process_map[e]).encode()).decode()

    def func_20(e: float, t: float, n: float, *args: float) -> None:
        if process_map[e] == process_map[t]:
            target = process_map[n]
            if callable(target):
                target(*[process_map[arg] for arg in args])

    def func_21(*_: Any) -> None:
        return

    def func_23(e: float, t: float, *args: float) -> None:
        if process_map[e] is not None and callable(process_map[t]):
            process_map[t](*args)

    def func_24(e: float, t: float, n: float) -> None:
        tv = process_map[t]
        nv = process_map[n]
        if isinstance(tv, str) and isinstance(nv, str):
            process_map[e] = f"{tv}.{nv}"

    process_map.update({
        1: func_1, 2: func_2, 3: func_3, 5: func_5, 6: func_6,
        7: func_7, 8: func_8, 9: token_list, 10: "window",
        14: func_14, 15: func_15, 16: p, 17: func_17,
        18: func_18, 19: func_19, 20: func_20, 21: func_21,
        23: func_23, 24: func_24,
    })

    for token in token_list:
        try:
            fn = process_map.get(token[0])
            if callable(fn):
                fn(*token[1:])
        except Exception:
            continue
    return result or None

# ---------------------------------------------------------------------------
# result dataclass
# ---------------------------------------------------------------------------

@dataclass
class SentinelTokenResult:
    token: str
    proof_token: str = ""
    turnstile_token: str = ""
    so_token: str = ""
    raw: Dict[str, Any] = field(default_factory=dict)

    def as_headers(self) -> Dict[str, str]:
        h = {"OpenAI-Sentinel-Chat-Requirements-Token": self.token}
        if self.proof_token:
            h["OpenAI-Sentinel-Proof-Token"] = self.proof_token
        if self.turnstile_token:
            h["OpenAI-Sentinel-Turnstile-Token"] = self.turnstile_token
        if self.so_token:
            h["OpenAI-Sentinel-SO-Token"] = self.so_token
        return h

# ---------------------------------------------------------------------------
# session + bootstrap
# ---------------------------------------------------------------------------

def _build_session(access_token: str = "") -> requests.Session:
    session = requests.Session(impersonate="firefox133")
    session.headers.update({
        "User-Agent": USER_AGENT,
        "Origin": BASE_URL,
        "Referer": BASE_URL + "/",
        "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8,en-US;q=0.7",
        "Cache-Control": "no-cache",
        "Pragma": "no-cache",
        "Priority": "u=1, I",
        "Sec-Ch-Ua": SEC_CH_UA,
        "Sec-Ch-Ua-Arch": '"x86"',
        "Sec-Ch-Ua-Bitness": '"64"',
        "Sec-Ch-Ua-Full-Version": '"143.0.3650.96"',
        "Sec-Ch-Ua-Full-Version-List": (
            '"Microsoft Edge";v="143.0.3650.96", '
            '"Chromium";v="143.0.7499.147", '
            '"Not A(Brand";v="24.0.0.0"'
        ),
        "Sec-Ch-Ua-Mobile": "?0",
        "Sec-Ch-Ua-Model": '""',
        "Sec-Ch-Ua-Platform": '"Windows"',
        "Sec-Ch-Ua-Platform-Version": '"19.0.0"',
        "Sec-Fetch-Dest": "empty",
        "Sec-Fetch-Mode": "cors",
        "Sec-Fetch-Site": "same-origin",
        "OAI-Device-Id": _new_uuid(),
        "OAI-Session-Id": _new_uuid(),
        "OAI-Language": "zh-CN",
        "OAI-Client-Version": CLIENT_VERSION,
        "OAI-Client-Build-Number": CLIENT_BUILD_NUMBER,
    })
    if access_token:
        session.headers["Authorization"] = f"Bearer {access_token}"
    return session

def _bootstrap(session: requests.Session) -> tuple[list[str], str]:
    resp = session.get(
        BASE_URL + "/",
        headers={
            "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
            "Sec-Fetch-Dest": "document",
            "Sec-Fetch-Mode": "navigate",
            "Sec-Fetch-Site": "none",
            "Sec-Fetch-User": "?1",
            "Upgrade-Insecure-Requests": "1",
        },
        timeout=30,
    )
    resp.raise_for_status()
    sources, data_build = _parse_pow_resources(resp.text)
    return sources or [DEFAULT_POW_SCRIPT], data_build

def _fetch_sentinel(
    session: requests.Session,
    script_sources: list[str],
    data_build: str,
    access_token: str = "",
) -> SentinelTokenResult:
    p_token = _build_legacy_requirements_token(USER_AGENT, script_sources, data_build)

    path = "/backend-api/sentinel/chat-requirements/prepare"
    resp = session.post(
        BASE_URL + path,
        headers={"Content-Type": "application/json", "X-OpenAI-Target-Path": path, "X-OpenAI-Target-Route": path},
        json={"p": p_token},
        timeout=30,
    )
    resp.raise_for_status()
    data = resp.json()
    prepare_token = data.get("prepare_token", "")

    if (data.get("arkose") or {}).get("required"):
        raise RuntimeError("sentinel endpoint requires arkose token (not implemented)")

    proof_token = ""
    proof_info = data.get("proofofwork") or {}
    if proof_info.get("required"):
        proof_token = _build_proof_token(
            proof_info.get("seed", ""),
            proof_info.get("difficulty", ""),
            USER_AGENT,
            script_sources=script_sources,
            data_build=data_build,
        )

    turnstile_token = ""
    turnstile_info = data.get("turnstile") or {}
    if turnstile_info.get("required") and turnstile_info.get("dx"):
        turnstile_token = _solve_turnstile_token(turnstile_info["dx"], p_token) or ""

    path = "/backend-api/sentinel/chat-requirements/finalize"
    resp = session.post(
        BASE_URL + path,
        headers={"Content-Type": "application/json", "X-OpenAI-Target-Path": path, "X-OpenAI-Target-Route": path},
        json={"prepare_token": prepare_token, "proofofwork": proof_token, "turnstile": turnstile_token},
        timeout=30,
    )
    resp.raise_for_status()
    data = resp.json()

    token = data.get("token", "")
    if not token:
        raise RuntimeError(f"missing sentinel token in response: {data}")

    return SentinelTokenResult(
        token=token,
        proof_token=proof_token,
        turnstile_token=turnstile_token,
        so_token=data.get("so_token", ""),
        raw=data,
    )

def get_sentinel_token(access_token: str = "") -> SentinelTokenResult:
    """Convenience: bootstrap + fetch sentinel token. Returns SentinelTokenResult."""
    session = _build_session(access_token)
    sources, data_build = _bootstrap(session)
    return _fetch_sentinel(session, sources, data_build, access_token)

# ---------------------------------------------------------------------------
# conversation
# ---------------------------------------------------------------------------

def _build_message(text: str, role: str = "user") -> Dict[str, Any]:
    return {
        "id": _new_uuid(),
        "author": {"role": role},
        "create_time": time.time(),
        "content": {"content_type": "text", "parts": [text]},
        "metadata": {
            "developer_mode_connector_ids": [],
            "selected_sources": [],
            "selected_github_repos": [],
            "selected_all_github_repos": False,
            "serialization_metadata": {"custom_symbol_offsets": []},
        },
    }

def _iter_sse(response: Any) -> Iterator[str]:
    for raw_line in response.iter_lines():
        if not raw_line:
            continue
        line = raw_line.decode("utf-8", errors="ignore") if isinstance(raw_line, bytes) else str(raw_line)
        if not line.startswith("data:"):
            continue
        payload = line[5:].strip()
        if payload:
            yield payload

def _extract_text(event: Dict[str, Any], current: str) -> str:
    """Extract assistant text from a raw SSE event, accumulating into `current`."""
    # Full message snapshot (most events carry the whole text in message.content.parts)
    for candidate in (event, event.get("v")):
        if not isinstance(candidate, dict):
            continue
        msg = candidate.get("message")
        if not isinstance(msg, dict):
            continue
        if (msg.get("author") or {}).get("role") != "assistant":
            continue
        parts = (msg.get("content") or {}).get("parts") or []
        text = "".join(p for p in parts if isinstance(p, str))
        if text:
            return text

    # JSON-patch style incremental updates
    if event.get("p") == "/message/content/parts/0":
        op, v = event.get("o"), str(event.get("v") or "")
        if op == "append":
            return current + v
        if op == "replace":
            return v

    if event.get("o") == "patch" and isinstance(event.get("v"), list):
        text = current
        for item in event["v"]:
            if isinstance(item, dict):
                text = _extract_text(item, text)
        return text

    # Plain string delta appended directly
    v = event.get("v")
    if isinstance(v, str) and not event.get("p") and not event.get("o") and current:
        return current + v

    return current

def _prepare_conversation(
    session: requests.Session,
    first_user_text: str,
    model: str,
    conversation_id: str = "",
    access_token: str = "",
) -> str:
    """POST /backend-api/f/conversation/prepare → conduit_token."""
    path = "/backend-api/f/conversation/prepare"
    tz = "Asia/Shanghai" if access_token else "America/Los_Angeles"
    tz_offset = -480 if access_token else 420
    body: Dict[str, Any] = {
        "action": "next",
        "fork_from_shared_post": False,
        "parent_message_id": "client-created-root",
        "model": model,
        "client_prepare_state": "none",
        "timezone_offset_min": tz_offset,
        "timezone": tz,
        "conversation_mode": {"kind": "primary_assistant"},
        "system_hints": [],
        "partial_query": {
            "id": _new_uuid(),
            "author": {"role": "user"},
            "content": {"content_type": "text", "parts": [first_user_text]},
        },
        "supports_buffering": True,
        "supported_encodings": ["v1"],
        "client_contextual_info": {"app_name": "chatgpt.com"},
    }
    if conversation_id:
        body["conversation_id"] = conversation_id
    resp = session.post(
        BASE_URL + path,
        headers={
            "Content-Type": "application/json",
            "Accept": "*/*",
            "X-Conduit-Token": "no-token",
            "X-OpenAI-Target-Path": path,
            "X-OpenAI-Target-Route": path,
        },
        json=body,
        timeout=60,
    )
    resp.raise_for_status()
    conduit_token = str(resp.json().get("conduit_token") or "")
    if not conduit_token:
        raise RuntimeError(f"missing conduit_token: {resp.text}")
    return conduit_token

def _stream_conversation(
    session: requests.Session,
    sentinel: SentinelTokenResult,
    conduit_token: str,
    messages: list[Dict[str, Any]],
    model: str,
    conversation_id: str = "",
    access_token: str = "",
) -> Iterator[tuple[str, str]]:
    """Yield (text_delta, conversation_id) until the stream ends."""
    path = "/backend-api/f/conversation"
    tz = "Asia/Shanghai" if access_token else "America/Los_Angeles"
    tz_offset = -480 if access_token else 420
    payload: Dict[str, Any] = {
        "action": "next",
        "messages": messages,
        "parent_message_id": "client-created-root",
        "model": model,
        "client_prepare_state": "success",
        "timezone_offset_min": tz_offset,
        "timezone": tz,
        "conversation_mode": {"kind": "primary_assistant"},
        "enable_message_followups": True,
        "system_hints": [],
        "supports_buffering": True,
        "supported_encodings": ["v1"],
        "client_contextual_info": {
            "is_dark_mode": False,
            "time_since_loaded": 120,
            "page_height": 900,
            "page_width": 1400,
            "pixel_ratio": 2,
            "screen_height": 1440,
            "screen_width": 2560,
            "app_name": "chatgpt.com",
        },
        "paragen_cot_summary_display_override": "allow",
        "force_parallel_switch": "auto",
    }
    if conversation_id:
        payload["conversation_id"] = conversation_id

    headers = {
        "Accept": "text/event-stream",
        "Content-Type": "application/json",
        "X-Conduit-Token": conduit_token,
        "X-OpenAI-Target-Path": path,
        "X-OpenAI-Target-Route": path,
        **sentinel.as_headers(),
    }

    resp = session.post(BASE_URL + path, headers=headers, json=payload, timeout=300, stream=True)
    resp.raise_for_status()

    current_text = ""
    active_cid = conversation_id

    try:
        for payload_str in _iter_sse(resp):
            if payload_str == "[DONE]":
                break
            try:
                event = json.loads(payload_str)
            except Exception:
                continue
            if not isinstance(event, dict):
                continue
            if event.get("type") in ("stream_handoff", "resume_conversation_token"):
                continue
            cid = event.get("conversation_id") or ""
            if cid:
                active_cid = cid
            new_text = _extract_text(event, current_text)
            if new_text != current_text:
                yield new_text[len(current_text):], active_cid
                current_text = new_text
    finally:
        resp.close()

# ---------------------------------------------------------------------------
# interactive chat
# ---------------------------------------------------------------------------

def chat(access_token: str = "", model: str = "auto") -> None:
    mode = "authenticated" if access_token else "anonymous"
    print(f"[ChatGPT CLI]  model={model}  mode={mode}")
    print("Type a message and press Enter. Empty line or Ctrl-C to quit.\n")

    session = _build_session(access_token)

    print("Bootstrapping...", end=" ", flush=True)
    sources, data_build = _bootstrap(session)
    print("ok")

    conversation_id = ""
    history: list[Dict[str, Any]] = []

    while True:
        try:
            user_input = input("You: ").strip()
        except (EOFError, KeyboardInterrupt):
            print()
            break
        if not user_input:
            break

        history.append(_build_message(user_input))

        # Fresh sentinel token every turn (they are short-lived)
        try:
            sentinel = _fetch_sentinel(session, sources, data_build, access_token)
        except Exception as e:
            print(f"[sentinel error] {e}")
            break

        try:
            conduit_token = _prepare_conversation(
                session, user_input, model, conversation_id, access_token
            )
        except Exception as e:
            print(f"[prepare error] {e}")
            break

        print("Assistant: ", end="", flush=True)
        assistant_text = ""
        try:
            for delta, cid in _stream_conversation(
                session, sentinel, conduit_token, history, model, conversation_id, access_token
            ):
                print(delta, end="", flush=True)
                assistant_text += delta
                if cid:
                    conversation_id = cid
        except Exception as e:
            print(f"\n[stream error] {e}")
        finally:
            print()

        if assistant_text:
            history.append(_build_message(assistant_text, "assistant"))

# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="ChatGPT CLI powered by sentinel token generation")
    parser.add_argument("--access-token", default="", help="Bearer access token (omit for anonymous)")
    parser.add_argument("--model", default="auto", help="Model slug, e.g. gpt-4o, auto (default: auto)")
    parser.add_argument(
        "--token-only",
        action="store_true",
        help="Just print the sentinel token and exit (no interactive chat)",
    )
    args = parser.parse_args()

    if args.token_only:
        result = get_sentinel_token(access_token=args.access_token)
        print("=== SentinelTokenResult ===")
        print(f"token          : {result.token}")
        print(f"proof_token    : {result.proof_token or '(not required)'}")
        print(f"turnstile_token: {result.turnstile_token or '(not required)'}")
        print(f"so_token       : {result.so_token or '(not present)'}")
        print()
        print("=== Headers ===")
        for k, v in result.as_headers().items():
            print(f"{k}: {v}")
    else:
        chat(access_token=args.access_token, model=args.model)
```

