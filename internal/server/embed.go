package server

import (
	_ "embed"
)

//go:embed web/index.html
var indexHTML []byte

//go:embed web/logo.png
var logoPNG []byte
