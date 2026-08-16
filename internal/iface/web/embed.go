// Встраивание статических ассетов Vue-бандла (Этап 8, make web-build).

package web

import "embed"

// assetsFS — встроенный бандл Web UI. Единственное package-level var
// в проекте помимо cmd/lentovodec.Version: директива //go:embed работает
// только с package-level переменными (зафиксировано в ROADMAP, Этап 7).
//
//go:embed assets
var assetsFS embed.FS
