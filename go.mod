module github.com/firefly-engineering/quartermaster

// 1.26, not the toolchain's 1.27: toolbox's golangci-lint is built with Go 1.26 and refuses a
// newer language version. Raise once toolbox rebuilds it:
// https://github.com/firefly-engineering/toolbox/issues/7
go 1.26
