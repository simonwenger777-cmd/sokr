package httpapi

import _ "embed"

//go:embed page.html
var Page []byte

//go:embed fold.js
var FoldJS []byte
