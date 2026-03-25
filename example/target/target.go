package main

import "C"
import "fmt"

// 示例目标 .so 库
// 编译命令: go build -buildmode=c-shared -o libtarget.so target.go

//export Add
func Add(a, b C.int) C.int {
	result := a + b
	fmt.Printf("[target] Add(%d, %d) = %d\n", a, b, result)
	return result
}

//export Multiply
func Multiply(a, b C.int) C.int {
	result := a * b
	fmt.Printf("[target] Multiply(%d, %d) = %d\n", a, b, result)
	return result
}

//export Hello
func Hello(name *C.char) {
	fmt.Printf("[target] Hello, %s!\n", C.GoString(name))
}

//export GetVersion
func GetVersion() *C.char {
	return C.CString("1.0.0")
}

func main() {}
