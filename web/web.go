// Package web — go:embed 内嵌前端页面, 编译产物为单一可执行文件
package web

import _ "embed"

//go:embed index.html
var IndexHTML []byte
