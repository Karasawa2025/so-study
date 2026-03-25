# so-study

研究使用 **Go** 编写 `.so` 转发器（proxy shared library）。

## 目标

实现一个完全用 Go（+ cgo）编写的工具链：

1. 读取任意目标 `A.so` 的导出函数列表（通过 ELF 动态符号表）
2. **自动生成一个 Go 转发器项目**，使用 `//export` + `buildmode=c-shared` 导出相同函数
3. 将 `A.so` 备份为 `B.so`，用编译后的转发器替换 `A.so`
4. 当外部程序调用 `A.so` 的函数时，Go 转发器会：
   - **打印一行日志**（时间戳 + 函数名）到 stderr
   - 通过 `dlopen`/`dlsym` 将调用**转发**到 `B.so` 的同名函数
   - **返回原始结果**

## 架构

```
调用方程序
    │
    ▼
┌──────────────────────────────────┐
│  A.so (Go 转发器)                │  ← Go + cgo 编译的 c-shared
│                                  │
│  //export Add                    │
│  func Add(a1, ...) {             │
│    1. log: "[so-proxy] -> Add()" │
│    2. C.trampoline_call(ptr, ..) │  ← 通过 C trampoline 转发
│  }                               │
│                                  │
│  initBackup():                   │
│    dlopen("B.so")                │
│    dlsym("Add"), dlsym("Mul")... │
└────────────────┬─────────────────┘
                 │
                 ▼
┌──────────────────────────────────┐
│  B.so (原始库备份)                │
│  实际函数实现                     │
└──────────────────────────────────┘
```

## 项目结构

```
so-study/
├── cmd/
│   └── so-proxy-gen/           # Go 代码生成器
│       └── main.go             #   读取 ELF 符号 → 生成 Go 转发器源码
├── example/
│   ├── target/                 # 示例目标库（Go buildmode=c-shared）
│   │   └── target.go           #   导出 Add, Multiply, Hello, GetVersion
│   └── test/                   # C 测试程序
│       └── test_proxy.c        #   通过 dlopen 调用 .so 函数
├── scripts/
│   └── demo.sh                 # 一键演示脚本
├── Makefile                    # 构建 & 演示
├── go.mod
└── README.md
```

## 快速开始

### 前置条件

- Go 1.21+（需要 cgo 支持）
- GCC（用于 cgo 编译和测试程序）
- Linux x86_64

### 运行演示

```bash
# 一键演示
make demo

# 或使用脚本
bash scripts/demo.sh
```

### 预期输出

```
=== .so 转发器测试 ===
加载库: ./libtarget.so

[so-proxy] 已加载备份库: ./libtarget_backup.so
[so-proxy] 2026-03-25 15:00:00 forwarding -> Add()
[target] Add(10, 20) = 30
Add(10, 20) = 30

[so-proxy] 2026-03-25 15:00:00 forwarding -> Multiply()
[target] Multiply(6, 7) = 42
Multiply(6, 7) = 42

[so-proxy] 2026-03-25 15:00:00 forwarding -> Hello()
[target] Hello, World!

[so-proxy] 2026-03-25 15:00:00 forwarding -> GetVersion()
GetVersion() = 1.0.0

=== 测试完成 ===
```

## 手动使用（对任意 .so）

```bash
# 1. 编译生成器
go build -o so-proxy-gen ./cmd/so-proxy-gen

# 2. 生成 Go 转发器项目
./so-proxy-gen -so /path/to/A.so -out ./proxy

# 3. 备份原始 .so
cp /path/to/A.so /path/to/A_backup.so

# 4. 编译转发器
cd proxy && CGO_ENABLED=1 go build -buildmode=c-shared -o A.so .

# 5. 用转发器替换原始 .so
cp proxy/A.so /path/to/A.so
```

## 技术细节

### 代码生成器 (`so-proxy-gen`)

使用 Go 标准库 `debug/elf` 解析目标 `.so` 的动态符号表：
- 提取 `STT_FUNC` + `STB_GLOBAL` 的函数符号
- 过滤掉 Go runtime / cgo 内部符号（`_cgo*`, `runtime.*` 等）
- 为每个函数生成对应的 Go `//export` 桩函数

### 生成的转发器结构

生成器输出一个完整的 Go 项目（`proxy/`），包含：

| 文件 | 作用 |
|------|------|
| `proxy.go` | 主逻辑：备份库加载（`dlopen`）、符号解析（`dlsym`）、日志打印 |
| `trampoline.go` | cgo 封装：C trampoline 函数 + 每个导出函数的 `//export` 桩 |
| `go.mod` | Go module 定义 |
| `Makefile` | 编译命令 |

### 转发机制

```go
// 每个导出函数的 Go 桩 (自动生成)
//export Add
func Add(a1, a2, a3, a4, a5, a6 C.longlong) C.longlong {
    initBackup()                    // 确保备份库已加载
    C.log_forward(C.CString("Add")) // 打印日志
    ptr := funcPtrs[0]              // 获取真实函数指针
    return C.trampoline_call(ptr, a1, a2, a3, a4, a5, a6) // C 层转发
}
```

C trampoline 使用函数指针类型转换 + 调用，利用 x86_64 System V ABI：
- 前 6 个整数/指针参数通过 `rdi, rsi, rdx, rcx, r8, r9` 传递
- 返回值通过 `rax` 返回
- `C.longlong` (int64) 兼容 int/pointer 类型参数

### 为什么需要 C trampoline？

Go 不能直接通过 `unsafe.Pointer` 调用 C 函数指针——必须经过 cgo 桥接。
C trampoline 函数接收 `void*` 函数指针，cast 为正确的函数指针类型后调用。

## 已知限制

- **平台**: 仅支持 Linux x86_64 ELF（不支持 macOS Mach-O / Windows PE）
- **参数类型**: 通用桩使用 6 个 `int64` 参数覆盖 SysV ABI 寄存器参数，适用于整数/指针参数。浮点参数（xmm 寄存器）需要额外处理
- **Go runtime 冲突**: 如果目标 .so 也是 Go 编译的，两个 Go runtime 会共存（可能有内存开销，但功能正常）
- **dlclose**: Go 编译的 .so 不支持安全卸载（Go runtime 限制）
- **函数签名**: 生成器无法自动推断精确的函数签名（ELF 符号表不包含类型信息）

## 改进方向

- [ ] 支持解析 DWARF 调试信息，自动推断函数签名
- [ ] 支持浮点参数的转发（xmm 寄存器）
- [ ] 支持配置文件指定已知函数的精确签名
- [ ] 支持 ARM64 (aarch64) 平台
- [ ] 添加调用计数/耗时统计功能

## License

MIT
