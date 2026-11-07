//go:build !linux || !cgo || gtk3

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// The desktop evaluation currently targets Linux GTK4 + WebKitGTK 6.0. Keep
// the module buildable elsewhere without pretending those builds have the
// native policy guard.
func installNavigationGuard(_ *application.WebviewWindow, _ string) bool { return true }
