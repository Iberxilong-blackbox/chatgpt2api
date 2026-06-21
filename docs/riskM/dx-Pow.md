2438141

在分析 ChatGPT 的 SentinelSDK 时，我们发现除了常规的 PoW（工作量证明）外，服务器还会下发一段经过加密的 dx 字符串。
与其他简单的 Base64 加密不同，dx 的解密密钥是动态的，它与我上一篇讲的 PoW Token 强绑定。

一、 核心架构：PoW 与 VM 的联动
dx 解密的核心在于密钥的获取。在 SDK 调用 Pn(t, n) 函数处理 dx 之前，它已经完成了 PoW 计算，并将计算出的 Token（即 cachedProof）存入了一个 WeakMap 中。

// 1. 在 Ie 函数中，计算完 PoW 后，将 Token 与上下文绑定
I(o.cachedChatReq, o.cachedProof); 
当进入 Pn 函数时，SDK 会通过一个 $ 函数从 WeakMap 中取出这个 PoW Token，作为解密 dx 的密钥。这种设计确保了只有真正完成了算力证明的客户端，才能解开后续的挑战指令。

二、 解密算法还原
dx 的解密过程分为三个步骤：Base64 解码 → XOR 异或解密 → JSON 解析。

1. Base64 解码
服务器下发的 dx 是一个 Base64 字符串。SDK 首先使用 atob 将其还原为二进制字符串。

const decodedStr = atob(dx_string);
2. XOR 异或解密
接下来是核心的异或解密。SDK 使用了一个名为 Rn 的函数。它将 Base64 解码后的字符串与 PoW Token 进行逐字符异或。如果 Token 长度不够，则通过取模运算 (o % n.length) 循环使用密钥。
算法还原：

function xorDecrypt(t, n) {
    let r = "";
    for (let o = 0; o < t.length; o++) {
        // 逐字符进行 XOR 异或运算
        r += String.fromCharCode(t.charCodeAt(o) ^ n.charCodeAt(o % n.length));
    }
    return r;
}
3. JSON 解析为指令数组
XOR 解密后得到的明文，实际上是一个 JSON 格式的字符串。它被解析为一个二维数组，这个数组就是虚拟机要执行的字节码指令集。

const instructions = JSON.parse(xorDecrypt(atob(dx_string), pow_token));
三、 完整解密流程代码
综合以上分析，我们可以还原出完整的解密调用链：

function decryptDx(dx_string, context) {
    // 1. 从上下文中提取 PoW Token 作为密钥
    const secretKey = getFromWeakMap(context) ?? ""; 
    
    try {
        // 2. Base64 解码
        const base64Decoded = atob(dx_string);
        
        // 3. XOR 异或解密
        let plaintext = "";
        for (let i = 0; i < base64Decoded.length; i++) {
            plaintext += String.fromCharCode(
                base64Decoded.charCodeAt(i) ^ secretKey.charCodeAt(i % secretKey.length)
            );
        }
        
        // 4. JSON 解析，得到 VM 指令数组
        const instructions = JSON.parse(plaintext);
        
        // 5. 将指令推入虚拟机的指令队列 (寄存器 9)
        VM_State.set(9, instructions);
        
        // 6. 启动虚拟机执行循环
        executeVMQueue();
        
    } catch (e) {
        // 解密或解析失败，返回错误信息
        return btoa("Decrypt Error: " + e);
    }
}
四、 VM 执行简述
SDK 内部维护了一个 Map 结构作为虚拟机的内存/寄存器。执行引擎会不断从指令队列（寄存器 9）中 shift() 出指令：

第一个元素是操作码（Opcode），例如 6 代表属性读取，3 代表 Resolve 返回。
后续元素是参数，通常指向其他寄存器的索引。
这些指令会被用来访问 window、navigator 等对象，进行更深层次的环境检测（如检测自动化工具特征），最终将收集到的数据通过 btoa 编码后返回给服务器。