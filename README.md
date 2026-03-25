# so-study

研究使用 Go 编写 `.so` 转发器（proxy shared library）。

## 目标

实现一个工具链，能够：

1. 读取任意目标 `A.so` 的导出函数列表
2. 自动生成一个"转发器" `.so`，导出与 `A.so` 完全相同的函数符号
3. 将 `A.so` 备份为 `B.so`，用转发器替换 `A.so`
4. 当外部程序调用 `A.so` 的函数时，转发器会：
   - **打印一行日志**（时间戳 + 函数名）到 stderr
   - 将调用**原样转发**到 `B.so` 的同名函数
   - **返回原始结果**

## 架构

```
调用方程序
    │
    ▼
┌──────────────────┐
│  A.so (转发器)    │  ← gcc 编译的 C 共享库
│                  │
│  每个导出函数:     │
│  1. 打印日志      │
│  2. dlopen B.so  │
│  3. dlsym 同名符号 │
│  4. 转发调用      │
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│  B.so (原始库)    │  ← 原始的 A.so 备份
│                  │
│  实际函数实现      │
└──────────────────┘
```

## 项目结构

```
so-study/
├── cmd/
│   └── so-proxy-gen/       # Go 代码生成器：读取 .so 符号，生成 C 转发器源码
│       └── main.go
├── example/
│   ├── target/             # 示例目标库（Go 编写的 .so）
│   │   └── target.go
│   └── test/               # C 测试程序
│       └── test_proxy.c
├── scripts/
│   └── demo.sh             # 一键演示脚本
├── Makefile                # 构建与演示
├── go.mod
└── README.md
```

## 快速开始

### 前置条件

- Go 1.21+
- GCC
- Linux x86_64（目前转发器基于 System V ABI）

### 运行演示

```bash
# 一键演示（编译 → 生成 → 替换 → 测试）
make demo

# 或使用脚本
bash scripts/demo.sh
```

### 预期输出

```
=== .so 转发器测试 ===
加载库: ./libtarget.so

[so-proxy] 2026-03-25 14:30:00 loaded backup library: ./libtarget_backup.so
[so-proxy] 2026-03-25 14:30:00 forwarding -> Add()
[target] Add(10, 20) = 30
Add(10, 20) = 30

[so-proxy] 2026-03-25 14:30:00 forwarding -> Multiply()
[target] Multiply(6, 7) = 42
Multiply(6, 7) = 42

[so-proxy] 2026-03-25 14:30:00 forwarding -> Hello()
[target] Hello, World!

[so-proxy] 2026-03-25 14:30:00 forwarding -> GetVersion()
GetVersion() = 1.0.0

=== 测试完成 ===
```

注意 `[so-proxy]` 开头的行即为转发器打印的日志。

## 手动使用

### 对任意 .so 生成转发器

```bash
# 1. 编译生成器
go build -o so-proxy-gen ./cmd/so-proxy-gen

# 2. 对目标 .so 生成转发器源码
./so-proxy-gen -so /path/to/A.so -out proxy.c

# 3. 备份原始 .so
cp /path/to/A.so /path/to/A_backup.so

# 4. 编译转发器替换原始 .so
gcc -shared -fPIC -o /path/to/A.so proxy.c -ldl
```

## 技术方案

### 符号提取

使用 Go 的 `debug/elf` 包读取目标 `.so` 的动态符号表，过滤出：
- 类型为 `STT_FUNC`（函数）
- 绑定为 `STB_GLOBAL`（全局导出）
- 排除 Go runtime、cgo 内部符号（以 `_cgo`、`runtime.` 等开头）

### 转发实现

生成的 `proxy.c` 中，每个导出函数被替换为一个转发桩：

```c
long Add(long a1, long a2, long a3, long a4, long a5, long a6) {
    // 延迟解析：首次调用时 dlsym
    if (pfn_Add == NULL)
        pfn_Add = resolve("Add");

    // 打印日志
    fprintf(stderr, "[so-proxy] %s forwarding -> Add()\n", timestamp);

    // 转发调用
    return pfn_Add(a1, a2, a3, a4, a5, a6);
}
```

### 两种转发方案

| 方案 | 优点 | 限制 |
|------|------|------|
| **A: C 函数（默认）** | 可移植、易理解 | 需要 6 个 `long` 参数覆盖 SysV ABI 寄存器参数 |
| **B: 汇编 jmp（注释中）** | 完美透传任意参数（含浮点、变参） | 仅 x86_64 Linux |

对于已知函数签名的场景，建议修改生成的 `typedef` 为精确类型。

## 已知限制

- 目前仅支持 ELF 格式（Linux），不支持 macOS 的 Mach-O
- 方案 A 的 6 参数 `long` 桩覆盖 x86_64 SysV 整数/指针参数，浮点参数可能需要方案 B
- Go 编译的 `.so` 不可安全 `dlclose`（Go runtime 限制）
- 生成器无法自动推断函数签名（C 级别的 ABI 限制），通用桩适用于大多数整数/指针参数函数

## License

MIT
